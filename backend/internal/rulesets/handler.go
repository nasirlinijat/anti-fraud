package rulesets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const maxBodyBytes = 64 << 10

// RegisterRoutes exposes the active ruleset and version publishing.
func RegisterRoutes(mux *http.ServeMux, store *Store, apiKey string) {
	mux.Handle("GET /rulesets/active", platform.RequireAPIKey(apiKey, http.HandlerFunc(store.getActive)))
	mux.Handle("POST /rulesets", platform.RequireAPIKey(apiKey, http.HandlerFunc(store.publish)))
}

type configPayload struct {
	ReviewThreshold int32                             `json:"review_threshold"`
	BlockThreshold  int32                             `json:"block_threshold"`
	Rules           map[scoring.RuleName]*rulePayload `json:"rules"`
}

type rulePayload struct {
	Enabled *bool  `json:"enabled"`
	Weight  *int32 `json:"weight"`
	Limit   *int64 `json:"limit"`
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

func (s *Store) publish(w http.ResponseWriter, r *http.Request) {
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
	rs, err := s.Publish(r.Context(), rs)
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
	if p.Rules == nil {
		fields["rules"] = "is required"
	}

	rs := scoring.Ruleset{
		ReviewThreshold: int(p.ReviewThreshold),
		BlockThreshold:  int(p.BlockThreshold),
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
		case *rule.Limit < 0:
			fields[field+".limit"] = "must be a non-negative integer"
		case name == scoring.RuleNewAccount && *rule.Limit > math.MaxInt64/int64(time.Hour):
			fields[field+".limit"] = "exceeds the maximum account age in hours"
		default:
			config.Limit = *rule.Limit
		}
		if config.Enabled {
			maxScore += int64(config.Weight)
		}
		rs.Rules[name] = config
	}
	if maxScore > math.MaxInt32 {
		fields["rules"] = fmt.Sprintf("enabled rule weights must add up to at most %d", math.MaxInt32)
	}
	return rs, fields
}

func writeRuleset(w http.ResponseWriter, status int, rs scoring.Ruleset) {
	rules := make(map[scoring.RuleName]*rulePayload, len(rs.Rules))
	for name, rule := range rs.Rules {
		weight := int32(rule.Weight)
		rules[name] = &rulePayload{Enabled: &rule.Enabled, Weight: &weight, Limit: &rule.Limit}
	}
	platform.WriteJSON(w, status, struct {
		Version int `json:"version"`
		configPayload
	}{
		Version: rs.Version,
		configPayload: configPayload{
			ReviewThreshold: int32(rs.ReviewThreshold),
			BlockThreshold:  int32(rs.BlockThreshold),
			Rules:           rules,
		},
	})
}
