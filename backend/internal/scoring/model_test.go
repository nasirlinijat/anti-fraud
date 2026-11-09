package scoring

import (
	"math"
	"math/rand/v2"
	"testing"
)

var testPolicy = ModelPolicy{FlagAt: 0.3, ClearBelow: 0.01}

func TestApplyModel(t *testing.T) {
	tests := []struct {
		outcome Outcome
		score   float64
		want    Outcome
	}{
		{OutcomeAllow, 0, OutcomeAllow},
		{OutcomeAllow, 0.2999, OutcomeAllow},
		{OutcomeAllow, 0.3, OutcomeReview},
		{OutcomeAllow, 1, OutcomeReview},
		{OutcomeReview, 0, OutcomeAllow},
		{OutcomeReview, 0.0099, OutcomeAllow},
		{OutcomeReview, 0.01, OutcomeReview},
		{OutcomeReview, 1, OutcomeReview},
		{OutcomeBlock, 0, OutcomeBlock},
		{OutcomeBlock, 1, OutcomeBlock},
	}
	for _, tt := range tests {
		if got := ApplyModel(tt.outcome, tt.score, testPolicy); got != tt.want {
			t.Errorf("ApplyModel(%s, %v) = %s, want %s", tt.outcome, tt.score, got, tt.want)
		}
	}
}

// TestModelNeverBlocks tries many scores and policies: the model never turns
// an outcome into a block and never changes a block.
func TestModelNeverBlocks(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 100000 {
		clear := rng.Float64()
		p := ModelPolicy{ClearBelow: clear, FlagAt: clear + rng.Float64()*(1-clear)}
		score := rng.Float64()
		for _, outcome := range []Outcome{OutcomeAllow, OutcomeReview, OutcomeBlock} {
			got := ApplyModel(outcome, score, p)
			if outcome != OutcomeBlock && got == OutcomeBlock {
				t.Fatalf("ApplyModel(%s, %v, %+v) = block", outcome, score, p)
			}
			if outcome == OutcomeBlock && got != OutcomeBlock {
				t.Fatalf("ApplyModel(block, %v, %+v) = %s, want block", score, p, got)
			}
		}
	}
}

func TestModelPolicyValidate(t *testing.T) {
	valid := []ModelPolicy{{FlagAt: 0.3, ClearBelow: 0.01}, {FlagAt: 1, ClearBelow: 0}}
	for _, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", p, err)
		}
	}
	invalid := []ModelPolicy{
		{FlagAt: 0.3, ClearBelow: 0.3},
		{FlagAt: 0.1, ClearBelow: 0.2},
		{FlagAt: 1.5, ClearBelow: 0.1},
		{FlagAt: 0.3, ClearBelow: -0.1},
		{},
		{FlagAt: math.NaN(), ClearBelow: 0.01},
	}
	for _, p := range invalid {
		if err := p.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want an error", p)
		}
	}
}
