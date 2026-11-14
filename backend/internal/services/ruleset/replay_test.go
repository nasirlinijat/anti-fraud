package ruleset

import (
	"slices"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

var (
	high     = scoring.FiredRule{Name: scoring.RuleHighAmount, Points: 30}
	velocity = scoring.FiredRule{Name: scoring.RuleCardVelocity, Points: 40}
	policy   = scoring.ModelPolicy{ClearBelow: 0.1, FlagAt: 0.9}
)

func scoredBy() scoring.Ruleset {
	clear, flag := 0.1, 0.9
	return scoring.Ruleset{
		Version: 3, ReviewThreshold: 50, BlockThreshold: 80,
		Rules: map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleHighAmount:   {Enabled: true, Weight: 30, Limit: 50000},
			scoring.RuleCardVelocity: {Enabled: true, Weight: 40, Limit: 3},
			scoring.RuleNewAccount:   {Enabled: false, Weight: 20, Limit: 7},
		},
		Model: scoring.ModelSettings{Mode: scoring.ModelAssist, ClearBelow: &clear, FlagAt: &flag},
	}
}

func decision(outcome string, model *float64, fired ...scoring.FiredRule) models.ReplayDecision {
	score := 0
	for _, f := range fired {
		score += f.Points
	}
	return models.ReplayDecision{Outcome: outcome, RuleScore: score, FiredRules: fired, ModelScore: model, RulesetVersion: 3}
}

func ptrTo(v float64) *float64 { return &v }

var (
	stayedReview = decision("review", ptrTo(0.5), high, velocity)
	flagged      = decision("review", ptrTo(0.95), velocity)
	cleared      = decision("allow", ptrTo(0.05), high, velocity)
	blocklisted  = decision("block", nil, scoring.BlocklistMatch("card"))
	quiet        = decision("allow", ptrTo(0.3))
)

func TestReplayWithTheSameRulesetGivesTheSavedOutcome(t *testing.T) {
	for _, d := range []models.ReplayDecision{stayedReview, flagged, cleared, blocklisted, quiet} {
		got := replay(d, scoredBy(), scoredBy(), &policy)
		if string(got.outcome) != d.Outcome || got.ruleScore != d.RuleScore || len(got.uncertainRules) != 0 || got.missingModelScore {
			t.Errorf("replay of %+v = %+v, want the saved outcome %s", d, got, d.Outcome)
		}
	}
}

func TestReplayAppliesPointsThresholdsAndSwitchedOffRules(t *testing.T) {
	morePoints := scoredBy()
	morePoints.Rules[scoring.RuleCardVelocity] = scoring.RuleConfig{Enabled: true, Weight: 50, Limit: 3}
	noHigh := scoredBy()
	noHigh.Rules[scoring.RuleHighAmount] = scoring.RuleConfig{Enabled: false, Weight: 30, Limit: 50000}
	lowerReview := scoredBy()
	lowerReview.ReviewThreshold = 30
	shadow := scoredBy()
	shadow.Model.Mode = scoring.ModelShadow

	cases := []struct {
		name     string
		draft    scoring.Ruleset
		decision models.ReplayDecision
		want     scoring.Outcome
		score    int
	}{
		{"more points push a review to block", morePoints, stayedReview, scoring.OutcomeBlock, 80},
		{"a switched off rule lowers the score", noHigh, stayedReview, scoring.OutcomeAllow, 40},
		{"a lower review threshold reviews more", lowerReview, decision("allow", ptrTo(0.3), velocity), scoring.OutcomeReview, 40},
		{"the model still flags an allowed purchase", noHigh, flagged, scoring.OutcomeReview, 40},
		{"shadow mode stops the model clearing reviews", shadow, cleared, scoring.OutcomeReview, 70},
		{"a blocklist match stays blocked", lowerReview, blocklisted, scoring.OutcomeBlock, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := replay(c.decision, scoredBy(), c.draft, &policy)
			if got.outcome != c.want || got.ruleScore != c.score || len(got.uncertainRules) != 0 {
				t.Fatalf("got %+v, want %s with %d points", got, c.want, c.score)
			}
		})
	}
}

func TestReplayNamesWhatItCannotKnow(t *testing.T) {
	newRule := scoredBy()
	newRule.Rules[scoring.RuleNewAccount] = scoring.RuleConfig{Enabled: true, Weight: 20, Limit: 7}
	newLimit := scoredBy()
	newLimit.Rules[scoring.RuleCardVelocity] = scoring.RuleConfig{Enabled: true, Weight: 40, Limit: 5}

	if got := replay(stayedReview, scoredBy(), newRule, &policy); !slices.Equal(got.uncertainRules, []scoring.RuleName{scoring.RuleNewAccount}) {
		t.Fatalf("a rule that was off: %+v", got)
	}
	if got := replay(stayedReview, scoredBy(), newLimit, &policy); !slices.Equal(got.uncertainRules, []scoring.RuleName{scoring.RuleCardVelocity}) {
		t.Fatalf("a changed limit: %+v", got)
	}
	if got := replay(decision("allow", nil), scoredBy(), scoredBy(), &policy); !got.missingModelScore {
		t.Fatalf("assist mode without a saved model score: %+v", got)
	}
	if got := replay(decision("allow", nil), scoredBy(), scoredBy(), nil); got.missingModelScore || got.outcome != scoring.OutcomeAllow {
		t.Fatalf("without a loaded model the rules decide alone: %+v", got)
	}
}
