package rulesets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const maxBodyBytes = 64 << 10
const maxDeviceSpikeWindowDays = 3650
const maxNoteRunes = 500

// RegisterRoutes exposes the active ruleset, version history, and version publishing.
func RegisterRoutes(mux *http.ServeMux, store *Store, apiKey string, operators platform.OperatorKeys) {
	mux.Handle("GET /rulesets", platform.RequireAPIKey(apiKey, http.HandlerFunc(store.list)))
	mux.Handle("GET /rulesets/active", platform.RequireAPIKey(apiKey, http.HandlerFunc(store.getActive)))
	mux.Handle("GET /rulesets/{version}", platform.RequireAPIKey(apiKey, http.HandlerFunc(store.getVersion)))
	mux.Handle("POST /rulesets", platform.RequireOperator(operators, store.publish))
}

type configPayload struct {
	ReviewThreshold int32                             `json:"review_threshold"`
	BlockThreshold  int32                             `json:"block_threshold"`
	Note            string                            `json:"note"`
	Rules           map[scoring.RuleName]*rulePayload `json:"rules"`
	Model           *modelPayload                     `json:"model,omitempty"`
}

// modelPayload is how a version uses the fraud model. Null thresholds in a
// response mean the version uses the thresholds of the loaded model's report.
type modelPayload struct {
	Mode       scoring.ModelMode `json:"mode"`
	ClearBelow *float64          `json:"clear_below"`
	FlagAt     *float64          `json:"flag_at"`
}

type rulePayload struct {
	Enabled          *bool  `json:"enabled"`
	Weight           *int32 `json:"weight"`
	Limit            *int64 `json:"limit"`
	WindowDays       *int32 `json:"window_days,omitempty"`
	MinAmountMinor   *int64 `json:"min_amount_minor,omitempty"`
	AmountMultiplier *int32 `json:"amount_multiplier,omitempty"`
}

func (s *Store) list(w http.ResponseWriter, r *http.Request) {
	versions, err := s.List(r.Context())
	if err != nil {
		log.Printf("list rulesets: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	platform.WriteJSON(w, http.StatusOK, struct {
		Versions []VersionSummary `json:"versions"`
	}{Versions: versions})
}

func (s *Store) getActive(w http.ResponseWriter, r *http.Request) {
	rs, err := s.Active(r.Context())
	if err != nil {
		log.Printf("read active ruleset: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeRuleset(w, http.StatusOK, rs)
}

func (s *Store) getVersion(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 32)
	if err != nil || version <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "the ruleset version must be a positive integer")
		return
	}
	rs, err := s.Version(r.Context(), int32(version))
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "no ruleset has this version")
		return
	}
	if err != nil {
		log.Printf("read ruleset %d: %v", version, err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeRuleset(w, http.StatusOK, rs)
}

func (s *Store) publish(w http.ResponseWriter, r *http.Request, operatorID string) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	var payload *configPayload
	if err := dec.Decode(&payload); err != nil {
		writeDecodeError(w, err)
		return
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != nil && err != io.EOF {
		writeDecodeError(w, err)
		return
	}
	if payload == nil || len(extra) != 0 {
		platform.WriteError(w, http.StatusBadRequest, "request body must contain a single valid ruleset object")
		return
	}

	rs, fields := payload.ruleset()
	if len(fields) > 0 {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: fields})
		return
	}
	rs, err := s.Publish(r.Context(), rs, operatorID)
	if err != nil {
		log.Printf("%v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeRuleset(w, http.StatusCreated, rs)
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	var wrongType *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooLarge):
		platform.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body must not exceed %d bytes", tooLarge.Limit))
	case errors.As(err, &wrongType) && wrongType.Field != "":
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{
			Error: "invalid request", Fields: map[string]string{wrongType.Field: "has the wrong type or is out of range"},
		})
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{
			Error: "invalid request", Fields: map[string]string{field: "is not a known field"},
		})
	default:
		platform.WriteError(w, http.StatusBadRequest, "request body must contain a single valid ruleset object")
	}
}

func (p configPayload) ruleset() (scoring.Ruleset, map[string]string) {
	fields := make(map[string]string)
	if p.ReviewThreshold <= 0 {
		fields["review_threshold"] = "must be a positive integer"
	}
	if p.BlockThreshold <= p.ReviewThreshold || p.BlockThreshold <= 0 {
		fields["block_threshold"] = "must be positive and greater than review_threshold"
	}
	note := strings.TrimSpace(p.Note)
	if utf8.RuneCountInString(note) > maxNoteRunes {
		fields["note"] = fmt.Sprintf("must be at most %d characters", maxNoteRunes)
	}
	if p.Rules == nil {
		fields["rules"] = "is required"
	}

	rs := scoring.Ruleset{
		ReviewThreshold: int(p.ReviewThreshold),
		BlockThreshold:  int(p.BlockThreshold),
		Note:            note,
		Rules:           make(map[scoring.RuleName]scoring.RuleConfig, len(p.Rules)),
	}
	var maxScore int64
	for name, rule := range p.Rules {
		field := "rules." + string(name)
		if !name.Known() {
			fields[field] = "is not a known rule"
			continue
		}
		if rule == nil {
			fields[field] = "must be a rule object"
			continue
		}

		var config scoring.RuleConfig
		if rule.Enabled == nil {
			fields[field+".enabled"] = "is required"
		} else {
			config.Enabled = *rule.Enabled
		}
		if rule.Weight == nil || *rule.Weight < 0 {
			fields[field+".weight"] = "must be a non-negative integer"
		} else {
			config.Weight = int(*rule.Weight)
		}
		switch {
		case rule.Limit == nil:
			if name != scoring.RuleCountryMismatch {
				fields[field+".limit"] = "is required"
			}
		case (name == scoring.RuleDeviceCardCount || name == scoring.RuleNewDeviceSpike) && *rule.Limit <= 0:
			fields[field+".limit"] = "must be a positive integer"
		case *rule.Limit < 0:
			fields[field+".limit"] = "must be a non-negative integer"
		case name == scoring.RuleNewAccount && *rule.Limit > math.MaxInt64/int64(time.Hour):
			fields[field+".limit"] = "exceeds the maximum account age in hours"
		default:
			config.Limit = *rule.Limit
		}
		if name == scoring.RuleNewDeviceSpike {
			if rule.WindowDays == nil || *rule.WindowDays < 1 || *rule.WindowDays > maxDeviceSpikeWindowDays {
				fields[field+".window_days"] = fmt.Sprintf("must be an integer from 1 to %d", maxDeviceSpikeWindowDays)
			} else {
				config.WindowDays = *rule.WindowDays
			}
			if rule.MinAmountMinor == nil || *rule.MinAmountMinor <= 0 {
				fields[field+".min_amount_minor"] = "must be a positive integer in minor units"
			} else {
				config.MinAmountMinor = *rule.MinAmountMinor
			}
			if rule.AmountMultiplier == nil || *rule.AmountMultiplier < 2 {
				fields[field+".amount_multiplier"] = "must be an integer of at least 2"
			} else {
				config.AmountMultiplier = *rule.AmountMultiplier
			}
		} else if rule.WindowDays != nil || rule.MinAmountMinor != nil || rule.AmountMultiplier != nil {
			fields[field] = "window_days, min_amount_minor, and amount_multiplier are only supported for new_device_spike"
		}
		if config.Enabled {
			maxScore += int64(config.Weight)
		}
		rs.Rules[name] = config
	}
	if maxScore > math.MaxInt32 {
		fields["rules"] = fmt.Sprintf("enabled rule weights must add up to at most %d", math.MaxInt32)
	}
	if p.Model != nil {
		rs.Model = p.Model.settings(fields)
	}
	return rs, fields
}

// settings validates the model settings and adds problems to fields.
func (m modelPayload) settings(fields map[string]string) scoring.ModelSettings {
	if !m.Mode.Known() {
		fields["model.mode"] = "must be off, shadow, or assist"
	}
	inRange := func(name string, v *float64) bool {
		if v != nil && (*v < 0 || *v > 1) {
			fields["model."+name] = "must be between 0 and 1"
			return false
		}
		return true
	}
	rangesValid := inRange("clear_below", m.ClearBelow)
	rangesValid = inRange("flag_at", m.FlagAt) && rangesValid
	switch {
	case (m.ClearBelow == nil) != (m.FlagAt == nil):
		fields["model"] = "clear_below and flag_at must be set together"
	case rangesValid && m.ClearBelow != nil && *m.ClearBelow >= *m.FlagAt:
		fields["model.clear_below"] = "must be lower than flag_at"
	}
	return scoring.ModelSettings{Mode: m.Mode, ClearBelow: m.ClearBelow, FlagAt: m.FlagAt}
}

func writeRuleset(w http.ResponseWriter, status int, rs scoring.Ruleset) {
	rules := make(map[scoring.RuleName]*rulePayload, len(rs.Rules))
	for name, rule := range rs.Rules {
		weight := int32(rule.Weight)
		payload := &rulePayload{Enabled: &rule.Enabled, Weight: &weight, Limit: &rule.Limit}
		if name == scoring.RuleNewDeviceSpike {
			payload.WindowDays = &rule.WindowDays
			payload.MinAmountMinor = &rule.MinAmountMinor
			payload.AmountMultiplier = &rule.AmountMultiplier
		}
		rules[name] = payload
	}
	platform.WriteJSON(w, status, struct {
		Version   int       `json:"version"`
		CreatedAt time.Time `json:"created_at"`
		configPayload
	}{
		Version:   rs.Version,
		CreatedAt: rs.CreatedAt.UTC(),
		configPayload: configPayload{
			ReviewThreshold: int32(rs.ReviewThreshold),
			BlockThreshold:  int32(rs.BlockThreshold),
			Note:            rs.Note,
			Rules:           rules,
			Model: &modelPayload{
				Mode:       rs.Model.Mode,
				ClearBelow: rs.Model.ClearBelow,
				FlagAt:     rs.Model.FlagAt,
			},
		},
	})
}
