package ruleset

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const (
	maxDeviceSpikeWindowDays = 3650
	maxNoteRunes             = 500
)

type Store interface {
	Active(ctx context.Context) (scoring.Ruleset, error)
	Version(ctx context.Context, version int32) (scoring.Ruleset, error)
	List(ctx context.Context) ([]models.RulesetSummary, error)
	Publish(ctx context.Context, rs scoring.Ruleset, operatorID string, reportThresholds *scoring.ModelSettings) (scoring.Ruleset, error)
}

// Draft is a ruleset as an operator submits it. Nil fields were left out.
type Draft struct {
	ReviewThreshold int32
	BlockThreshold  int32
	Note            string
	Rules           map[scoring.RuleName]*DraftRule
	Model           *DraftModel
}

type DraftRule struct {
	Enabled          *bool
	Weight           *int32
	Limit            *int64
	WindowDays       *int32
	MinAmountMinor   *int64
	AmountMultiplier *int32
}

type DraftModel struct {
	Mode       scoring.ModelMode
	ClearBelow *float64
	FlagAt     *float64
}

// ValidationError lists the problems found in a draft, keyed by field name.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return "invalid ruleset"
}

type Service struct {
	store Store
	// reportThresholds holds the thresholds from the loaded model's report.
	// Publish saves them in a version that has none. It is nil without a model.
	reportThresholds *scoring.ModelSettings
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// UseModelThresholds makes Publish save the thresholds of the loaded model's
// report in a new version that does not set its own.
func (s *Service) UseModelThresholds(p scoring.ModelPolicy) {
	clearBelow, flagAt := p.ClearBelow, p.FlagAt
	s.reportThresholds = &scoring.ModelSettings{ClearBelow: &clearBelow, FlagAt: &flagAt}
}

func (s *Service) Active(ctx context.Context) (scoring.Ruleset, error) {
	return s.store.Active(ctx)
}

func (s *Service) Version(ctx context.Context, version int32) (scoring.Ruleset, error) {
	return s.store.Version(ctx, version)
}

func (s *Service) List(ctx context.Context) ([]models.RulesetSummary, error) {
	return s.store.List(ctx)
}

// Publish validates the draft and saves it as the new active version. It
// returns a *ValidationError for an invalid draft.
func (s *Service) Publish(ctx context.Context, d Draft, operatorID string) (scoring.Ruleset, error) {
	rs, fields := d.ruleset()
	if len(fields) > 0 {
		return scoring.Ruleset{}, &ValidationError{Fields: fields}
	}
	return s.store.Publish(ctx, rs, operatorID, s.reportThresholds)
}

func (d Draft) ruleset() (scoring.Ruleset, map[string]string) {
	fields := make(map[string]string)
	if d.ReviewThreshold <= 0 {
		fields["review_threshold"] = "must be a positive integer"
	}
	if d.BlockThreshold <= d.ReviewThreshold || d.BlockThreshold <= 0 {
		fields["block_threshold"] = "must be positive and greater than review_threshold"
	}
	note := strings.TrimSpace(d.Note)
	if utf8.RuneCountInString(note) > maxNoteRunes {
		fields["note"] = fmt.Sprintf("must be at most %d characters", maxNoteRunes)
	}
	if d.Rules == nil {
		fields["rules"] = "is required"
	}

	rs := scoring.Ruleset{
		ReviewThreshold: int(d.ReviewThreshold),
		BlockThreshold:  int(d.BlockThreshold),
		Note:            note,
		Rules:           make(map[scoring.RuleName]scoring.RuleConfig, len(d.Rules)),
	}
	var maxScore int64
	for name, rule := range d.Rules {
		field := "rules." + string(name)
		if !name.Known() {
			fields[field] = "is not a known rule"
			continue
		}
		if rule == nil {
			fields[field] = "must be a rule object"
			continue
		}

		config := rule.config(name, field, fields)
		if config.Enabled {
			maxScore += int64(config.Weight)
		}
		rs.Rules[name] = config
	}
	if maxScore > math.MaxInt32 {
		fields["rules"] = fmt.Sprintf("enabled rule weights must add up to at most %d", math.MaxInt32)
	}
	if d.Model != nil {
		rs.Model = d.Model.settings(fields)
	}
	return rs, fields
}

// config validates one rule and converts it. Problems are added to fields
// under the rule's field name.
func (rule *DraftRule) config(name scoring.RuleName, field string, fields map[string]string) scoring.RuleConfig {
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
	config.Limit = rule.limit(name, field, fields)
	if name == scoring.RuleNewDeviceSpike {
		rule.spikeSettings(&config, field, fields)
	} else if rule.WindowDays != nil || rule.MinAmountMinor != nil || rule.AmountMultiplier != nil {
		fields[field] = "window_days, min_amount_minor, and amount_multiplier are only supported for new_device_spike"
	}
	return config
}

// limit returns the rule's limit, or zero with a problem added to fields.
// Only country_mismatch may leave the limit out.
func (rule *DraftRule) limit(name scoring.RuleName, field string, fields map[string]string) int64 {
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
		return *rule.Limit
	}
	return 0
}

// spikeSettings validates the settings that only new_device_spike has and
// copies the valid ones to config.
func (rule *DraftRule) spikeSettings(config *scoring.RuleConfig, field string, fields map[string]string) {
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
}

// settings validates the model settings and adds problems to fields.
func (m DraftModel) settings(fields map[string]string) scoring.ModelSettings {
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
