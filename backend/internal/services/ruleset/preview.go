package ruleset

import (
	"context"
	"fmt"
	"slices"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const (
	PreviewDecisions = 1000
	previewChanges   = 50
)

func (s *Service) Preview(ctx context.Context, d Draft) (models.RulesetPreview, error) {
	draft, fields := d.ruleset()
	if len(fields) > 0 {
		return models.RulesetPreview{}, &ValidationError{Fields: fields}
	}
	if draft.Model.Mode == "" {
		active, err := s.store.Active(ctx)
		if err != nil {
			return models.RulesetPreview{}, fmt.Errorf("read the active ruleset: %w", err)
		}
		draft.Model = active.Model
	}
	var policy *scoring.ModelPolicy
	if s.reportThresholds != nil {
		p := draft.Model.Policy(scoring.ModelPolicy{ClearBelow: *s.reportThresholds.ClearBelow, FlagAt: *s.reportThresholds.FlagAt})
		policy = &p
	}

	decisions, err := s.store.RecentDecisions(ctx, PreviewDecisions)
	if err != nil {
		return models.RulesetPreview{}, fmt.Errorf("read recent decisions: %w", err)
	}
	versions := map[int]scoring.Ruleset{}
	preview := models.RulesetPreview{Decisions: len(decisions), Changes: []models.PreviewChange{}, UncertainRules: []string{}}
	for _, decision := range decisions {
		scoredBy, ok := versions[decision.RulesetVersion]
		if !ok {
			if scoredBy, err = s.store.Version(ctx, int32(decision.RulesetVersion)); err != nil {
				return models.RulesetPreview{}, fmt.Errorf("read ruleset version %d: %w", decision.RulesetVersion, err)
			}
			versions[decision.RulesetVersion] = scoredBy
		}
		result := replay(decision, scoredBy, draft, policy)
		preview.Before.Add(decision.Outcome)
		preview.After.Add(string(result.outcome))
		if result.missingModelScore {
			preview.MissingModelScores++
		}
		uncertain := result.missingModelScore || len(result.uncertainRules) > 0
		if uncertain {
			preview.Uncertain++
		}
		for _, name := range result.uncertainRules {
			if !slices.Contains(preview.UncertainRules, string(name)) {
				preview.UncertainRules = append(preview.UncertainRules, string(name))
			}
		}
		if string(result.outcome) == decision.Outcome {
			continue
		}
		preview.Changed++
		if len(preview.Changes) < previewChanges {
			preview.Changes = append(preview.Changes, models.PreviewChange{
				PurchaseID: decision.PurchaseID, TransactionID: decision.TransactionID, DecidedAt: decision.DecidedAt,
				Before: decision.Outcome, After: string(result.outcome),
				RuleScoreBefore: decision.RuleScore, RuleScoreAfter: result.ruleScore, Uncertain: uncertain,
			})
		}
	}
	slices.Sort(preview.UncertainRules)
	return preview, nil
}

type replayResult struct {
	outcome           scoring.Outcome
	ruleScore         int
	uncertainRules    []scoring.RuleName
	missingModelScore bool
}

func replay(d models.ReplayDecision, scoredBy, draft scoring.Ruleset, policy *scoring.ModelPolicy) replayResult {
	fired := map[scoring.RuleName]bool{}
	for _, rule := range d.FiredRules {
		if rule.Name == scoring.RuleBlocklist {
			return replayResult{outcome: scoring.OutcomeBlock, ruleScore: d.RuleScore}
		}
		fired[rule.Name] = true
	}
	var result replayResult
	for name, cfg := range draft.Rules {
		if !cfg.Enabled {
			continue
		}
		if old, ok := scoredBy.Rules[name]; !ok || !old.Enabled || old.Limit != cfg.Limit || old.WindowDays != cfg.WindowDays ||
			old.MinAmountMinor != cfg.MinAmountMinor || old.AmountMultiplier != cfg.AmountMultiplier {
			result.uncertainRules = append(result.uncertainRules, name)
		}
		if fired[name] {
			result.ruleScore += cfg.Weight
		}
	}
	slices.Sort(result.uncertainRules)
	switch {
	case result.ruleScore >= draft.BlockThreshold:
		result.outcome = scoring.OutcomeBlock
	case result.ruleScore >= draft.ReviewThreshold:
		result.outcome = scoring.OutcomeReview
	default:
		result.outcome = scoring.OutcomeAllow
	}
	if draft.Model.Mode == scoring.ModelAssist && policy != nil {
		if d.ModelScore == nil {
			result.missingModelScore = true
		} else {
			result.outcome = scoring.ApplyModel(result.outcome, *d.ModelScore, *policy)
		}
	}
	return result
}
