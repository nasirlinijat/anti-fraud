package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
)

const maxRulesetBodyBytes = 64 << 10

type RulesetService interface {
	Active(ctx context.Context) (scoring.Ruleset, error)
	Version(ctx context.Context, version int32) (scoring.Ruleset, error)
	List(ctx context.Context) ([]models.RulesetSummary, error)
	Publish(ctx context.Context, d ruleset.Draft, operatorID string) (scoring.Ruleset, error)
}

type RulesetHandler struct {
	service RulesetService
}

func NewRulesetHandler(service RulesetService) *RulesetHandler {
	return &RulesetHandler{service: service}
}

// RegisterRulesetRoutes exposes the active ruleset, version history, and version publishing.
func RegisterRulesetRoutes(mux *http.ServeMux, h *RulesetHandler, access platform.Access) {
	mux.Handle("GET /rulesets", access.ConfigRead(http.HandlerFunc(h.list)))
	mux.Handle("GET /rulesets/active", access.ConfigRead(http.HandlerFunc(h.getActive)))
	mux.Handle("GET /rulesets/{version}", access.ConfigRead(http.HandlerFunc(h.getVersion)))
	mux.Handle("POST /rulesets", access.ConfigWrite(h.publish))
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

func (h *RulesetHandler) list(w http.ResponseWriter, r *http.Request) {
	versions, err := h.service.List(r.Context())
	if err != nil {
		log.Printf("list rulesets: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	platform.WriteJSON(w, http.StatusOK, struct {
		Versions []models.RulesetSummary `json:"versions"`
	}{Versions: versions})
}

func (h *RulesetHandler) getActive(w http.ResponseWriter, r *http.Request) {
	rs, err := h.service.Active(r.Context())
	if err != nil {
		log.Printf("read active ruleset: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeRuleset(w, http.StatusOK, rs)
}

func (h *RulesetHandler) getVersion(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 32)
	if err != nil || version <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "the ruleset version must be a positive integer")
		return
	}
	rs, err := h.service.Version(r.Context(), int32(version))
	if errors.Is(err, models.ErrRulesetNotFound) {
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

func (h *RulesetHandler) publish(w http.ResponseWriter, r *http.Request, operatorID string) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRulesetBodyBytes))
	dec.DisallowUnknownFields()
	var payload *configPayload
	if err := dec.Decode(&payload); err != nil {
		writeRulesetDecodeError(w, err)
		return
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != nil && err != io.EOF {
		writeRulesetDecodeError(w, err)
		return
	}
	if payload == nil || len(extra) != 0 {
		platform.WriteError(w, http.StatusBadRequest, "request body must contain a single valid ruleset object")
		return
	}

	rs, err := h.service.Publish(r.Context(), payload.draft(), operatorID)
	var invalid *ruleset.ValidationError
	if errors.As(err, &invalid) {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: invalid.Fields})
		return
	}
	if err != nil {
		log.Printf("%v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeRuleset(w, http.StatusCreated, rs)
}

func (p configPayload) draft() ruleset.Draft {
	d := ruleset.Draft{
		ReviewThreshold: p.ReviewThreshold,
		BlockThreshold:  p.BlockThreshold,
		Note:            p.Note,
	}
	if p.Rules != nil {
		d.Rules = make(map[scoring.RuleName]*ruleset.DraftRule, len(p.Rules))
		for name, rule := range p.Rules {
			if rule == nil {
				d.Rules[name] = nil
				continue
			}
			d.Rules[name] = &ruleset.DraftRule{
				Enabled:          rule.Enabled,
				Weight:           rule.Weight,
				Limit:            rule.Limit,
				WindowDays:       rule.WindowDays,
				MinAmountMinor:   rule.MinAmountMinor,
				AmountMultiplier: rule.AmountMultiplier,
			}
		}
	}
	if p.Model != nil {
		d.Model = &ruleset.DraftModel{Mode: p.Model.Mode, ClearBelow: p.Model.ClearBelow, FlagAt: p.Model.FlagAt}
	}
	return d
}

func writeRulesetDecodeError(w http.ResponseWriter, err error) {
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
