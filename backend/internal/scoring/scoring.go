package scoring

import "time"

// Outcome is the decision for a scored purchase.
type Outcome string

const (
	OutcomeAllow  Outcome = "allow"
	OutcomeReview Outcome = "review"
	OutcomeBlock  Outcome = "block"
)

// Input is the purchase data the rules check. The caller provides history counts.
type Input struct {
	AmountMinor           int64
	CardCountry           string
	IPCountry             *string
	AccountCreatedAt      *time.Time
	EventTime             time.Time
	CardPurchasesLastHour int // earlier purchases in the hour before EventTime
}

// RuleConfig is the setting for one rule.
type RuleConfig struct {
	Enabled bool
	Weight  int   // points added when the rule fires
	Limit   int64 // rule threshold; its unit depends on the rule
}

// Ruleset holds the rules and decision thresholds. A rule missing from Rules is disabled.
type Ruleset struct {
	Version         int
	ReviewThreshold int
	BlockThreshold  int
	Rules           map[RuleName]RuleConfig
}

// FiredRule is a rule that fired and the points it added.
type FiredRule struct {
	Name   RuleName
	Points int
}

// Result is the scoring decision for one purchase.
type Result struct {
	Outcome    Outcome
	Score      int
	FiredRules []FiredRule
}

// Evaluate runs the enabled rules against a purchase and decides its outcome.
func Evaluate(in Input, rs Ruleset) Result {
	var res Result
	for _, r := range rules {
		cfg, ok := rs.Rules[r.name]
		if !ok || !cfg.Enabled {
			continue
		}
		if r.check(in, cfg.Limit) {
			res.Score += cfg.Weight
			res.FiredRules = append(res.FiredRules, FiredRule{Name: r.name, Points: cfg.Weight})
		}
	}
	res.Outcome = decide(res.Score, rs)
	return res
}

func decide(score int, rs Ruleset) Outcome {
	switch {
	case score >= rs.BlockThreshold:
		return OutcomeBlock
	case score >= rs.ReviewThreshold:
		return OutcomeReview
	default:
		return OutcomeAllow
	}
}
