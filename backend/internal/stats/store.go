package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// RuleCount is how often a rule fired and the points it added in total.
type RuleCount struct {
	Name   string `json:"name"`
	Fired  int64  `json:"fired"`
	Points int64  `json:"points"`
}

// Outcomes counts decisions by outcome.
type Outcomes struct {
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

// Summary is the dashboard data for a decision window.
type Summary struct {
	Outcomes            Outcomes       `json:"outcomes"`
	TopRules            []RuleCount    `json:"top_rules"`
	ReviewQueue         int64          `json:"review_queue"`
	Model               ModelEffects   `json:"model"`
	P95ScoringLatencyUs *int64         `json:"p95_scoring_latency_us"`
	BlockedValueMinor   string         `json:"blocked_value_minor"`
	DecisionSeries      DecisionSeries `json:"decision_series"`
}

// Store reads decision statistics.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Summary reads one snapshot so decisions arriving during the poll cannot make
// the timeline and totals disagree. The review queue size is always global.
func (s *Store) Summary(ctx context.Context, window Window, maxRules int) (Summary, error) {
	var sum Summary
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		sum, err = readSummary(ctx, tx, window, maxRules)
		return err
	})
	return sum, err
}

// DecisionOverviewSQL counts the decisions in [$1, $2) by outcome and finds the
// 95th percentile scoring latency and the first decision time ($1 when there
// are none).
const DecisionOverviewSQL = `
SELECT
    count(*) FILTER (WHERE outcome = 'allow') AS allowed,
    count(*) FILTER (WHERE outcome = 'review') AS reviewed,
    count(*) FILTER (WHERE outcome = 'block') AS blocked,
    count(*) AS total,
    count(scoring_latency_us) AS latency_samples,
    coalesce(percentile_disc(0.95) WITHIN GROUP (ORDER BY scoring_latency_us), 0)::bigint AS p95_latency_us,
    coalesce(min(decided_at), $1::timestamptz)::timestamptz AS first_decision_at
FROM decisions
WHERE decided_at >= $1::timestamptz
  AND decided_at < $2::timestamptz`

// overview is a DecisionOverviewSQL row.
type overview struct {
	Outcomes
	latencySamples  int64
	p95LatencyUs    int64
	firstDecisionAt time.Time
}

// BlockedValueSQL sums blocked amounts as text, so the total cannot overflow.
const BlockedValueSQL = `
SELECT coalesce(sum(p.amount_minor), 0)::text AS amount_minor
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.outcome = 'block'
  AND d.decided_at >= $1::timestamptz
  AND d.decided_at < $2::timestamptz`

// TopFiredRulesSQL counts how often each rule fired in [$1, $2) and the
// points it added, most frequent first.
const TopFiredRulesSQL = `
SELECT
    rule.name::text AS name,
    count(*) AS fired,
    sum(rule.points)::bigint AS points
FROM decisions d
CROSS JOIN LATERAL jsonb_to_recordset(d.fired_rules) AS rule(name text, points integer)
WHERE d.decided_at >= $1::timestamptz
  AND d.decided_at < $2::timestamptz
GROUP BY rule.name
ORDER BY fired DESC, name
LIMIT $3`

// CountReviewQueueSQL counts the purchases still awaiting review.
const CountReviewQueueSQL = `
SELECT count(*)
FROM decisions d
WHERE d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)`

// CountModelEffectsSQL counts scored decisions, and decisions whose outcome
// differs from what the rule score alone gives.
const CountModelEffectsSQL = `
SELECT
    count(d.model_score) AS scored,
    count(*) FILTER (WHERE d.outcome = 'review' AND d.rule_score < r.review_threshold) AS flagged,
    count(*) FILTER (WHERE d.outcome = 'allow' AND d.rule_score >= r.review_threshold) AS cleared
FROM decisions d
JOIN rulesets r ON r.version = d.ruleset_version
WHERE d.decided_at >= $1::timestamptz
  AND d.decided_at < $2::timestamptz`

func readSummary(ctx context.Context, conn platform.DBTX, window Window, maxRules int) (Summary, error) {
	since, until := window.Since, window.Until
	var o overview
	err := conn.QueryRow(ctx, DecisionOverviewSQL, since, until).Scan(
		&o.Allow, &o.Review, &o.Block, &o.Total, &o.latencySamples, &o.p95LatencyUs, &o.firstDecisionAt,
	)
	if err != nil {
		return Summary{}, fmt.Errorf("read decision overview: %w", err)
	}
	sum := Summary{Outcomes: o.Outcomes, TopRules: []RuleCount{}}
	if o.latencySamples > 0 {
		sum.P95ScoringLatencyUs = &o.p95LatencyUs
	}
	if err := conn.QueryRow(ctx, BlockedValueSQL, since, until).Scan(&sum.BlockedValueMinor); err != nil {
		return Summary{}, fmt.Errorf("sum blocked value: %w", err)
	}
	if sum.DecisionSeries, err = readDecisionSeries(ctx, conn, window, o); err != nil {
		return Summary{}, fmt.Errorf("read decision buckets: %w", err)
	}

	rows, err := conn.Query(ctx, TopFiredRulesSQL, since, until, int32(maxRules))
	if err != nil {
		return Summary{}, fmt.Errorf("count fired rules: %w", err)
	}
	sum.TopRules, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (RuleCount, error) {
		var r RuleCount
		return r, row.Scan(&r.Name, &r.Fired, &r.Points)
	})
	if err != nil {
		return Summary{}, fmt.Errorf("count fired rules: %w", err)
	}

	if err := conn.QueryRow(ctx, CountReviewQueueSQL).Scan(&sum.ReviewQueue); err != nil {
		return Summary{}, fmt.Errorf("count review queue: %w", err)
	}
	if err := conn.QueryRow(ctx, CountModelEffectsSQL, since, until).Scan(&sum.Model.Scored, &sum.Model.Flagged, &sum.Model.Cleared); err != nil {
		return Summary{}, fmt.Errorf("count model effects: %w", err)
	}
	return sum, nil
}
