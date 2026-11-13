package models

import "time"

// StatsWindow covers decisions from Since (inclusive) to Until (exclusive).
type StatsWindow struct {
	Since, Until time.Time
	// BucketWidth is zero when the store should choose a width for all time.
	BucketWidth time.Duration
}

// RuleCount is how often a rule fired and the points it added in total.
type RuleCount struct {
	Name   string `json:"name"`
	Fired  int64  `json:"fired"`
	Points int64  `json:"points"`
}

// OutcomeCounts counts decisions by outcome.
type OutcomeCounts struct {
	Allow  int64 `json:"allow"`
	Review int64 `json:"review"`
	Block  int64 `json:"block"`
	Total  int64 `json:"total"`
}

// ModelEffects counts what the fraud model did.
type ModelEffects struct {
	// Scored is the number of decisions with a model score.
	Scored int64 `json:"scored"`
	// Flagged is the number of purchases the rules allowed and the model sent to review.
	Flagged int64 `json:"flagged"`
	// Cleared is the number of purchases the rules sent to review and the model allowed.
	Cleared int64 `json:"cleared"`
}

// StatsSummary is the dashboard data for a decision window.
type StatsSummary struct {
	Outcomes            OutcomeCounts  `json:"outcomes"`
	TopRules            []RuleCount    `json:"top_rules"`
	ReviewQueue         int64          `json:"review_queue"`
	Model               ModelEffects   `json:"model"`
	P95ScoringLatencyUs *int64         `json:"p95_scoring_latency_us"`
	BlockedValueMinor   string         `json:"blocked_value_minor"`
	DecisionSeries      DecisionSeries `json:"decision_series"`
	// BlocklistBlocks is the number of blocked decisions that matched the
	// blocklist. The other blocks came from the rules.
	BlocklistBlocks int64 `json:"blocklist_blocks"`
}

// DecisionBucket counts the decisions of each outcome in one time bucket.
type DecisionBucket struct {
	Start  time.Time `json:"start"`
	Allow  int64     `json:"allow"`
	Review int64     `json:"review"`
	Block  int64     `json:"block"`
}

// DecisionSeries is the decision counts of a period, in buckets of equal width.
type DecisionSeries struct {
	BucketSeconds int64            `json:"bucket_seconds"`
	Buckets       []DecisionBucket `json:"buckets"`
}
