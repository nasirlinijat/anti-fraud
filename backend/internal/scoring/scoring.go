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
