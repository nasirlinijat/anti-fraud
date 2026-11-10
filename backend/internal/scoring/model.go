package scoring

import (
	"fmt"
	"math"
)

// ModelPolicy decides how the fraud model score changes the rule outcome.
// Scores run from 0 to 1.
type ModelPolicy struct {
	// FlagAt sends an allowed purchase to review when the score is at or above it.
	FlagAt float64
	// ClearBelow allows a purchase the rules sent to review when the score is below it.
	ClearBelow float64
	// PlayerBlendWeight is the weight of the player's earlier average score in
	// the score the thresholds apply to; 0 turns blending off.
	PlayerBlendWeight float64
}

// Validate reports whether the thresholds are usable: both between 0 and 1,
// and ClearBelow below FlagAt.
func (p ModelPolicy) Validate() error {
	if math.IsNaN(p.FlagAt) || math.IsNaN(p.ClearBelow) || p.ClearBelow < 0 || p.FlagAt > 1 || p.ClearBelow >= p.FlagAt {
		return fmt.Errorf("model policy needs 0 <= clear_below < flag_at <= 1, got clear_below %v and flag_at %v", p.ClearBelow, p.FlagAt)
	}
	if math.IsNaN(p.PlayerBlendWeight) || p.PlayerBlendWeight < 0 || p.PlayerBlendWeight >= 1 {
		return fmt.Errorf("model policy needs 0 <= player_blend_weight < 1, got %v", p.PlayerBlendWeight)
	}
	return nil
}

// Blend mixes a model score with the average of the player's earlier scores.
// With no earlier scores the score is returned unchanged.
func (p ModelPolicy) Blend(score, earlierSum float64, earlierCount int64) float64 {
	if p.PlayerBlendWeight == 0 || earlierCount == 0 {
		return score
	}
	return (1-p.PlayerBlendWeight)*score + p.PlayerBlendWeight*(earlierSum/float64(earlierCount))
}

// ApplyModel returns the outcome after the model score. The model can clear a
// review or flag an allowed purchase for review, but it never blocks a purchase
// and never changes a block.
func ApplyModel(outcome Outcome, score float64, p ModelPolicy) Outcome {
	switch {
	case outcome == OutcomeReview && score < p.ClearBelow:
		return OutcomeAllow
	case outcome == OutcomeAllow && score >= p.FlagAt:
		return OutcomeReview
	default:
		return outcome
	}
}
