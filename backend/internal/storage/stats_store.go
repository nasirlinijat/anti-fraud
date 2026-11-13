package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// StatsStore reads decision statistics.
type StatsStore struct {
	pool *pgxpool.Pool
}

func NewStatsStore(pool *pgxpool.Pool) *StatsStore {
	return &StatsStore{pool: pool}
}

// Summary reads one snapshot so decisions arriving during the poll cannot make
// the timeline and totals disagree. The review queue size is always global.
func (s *StatsStore) Summary(ctx context.Context, window models.StatsWindow, maxRules int) (models.StatsSummary, error) {
	var sum models.StatsSummary
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		sum, err = readSummary(ctx, tx, window, maxRules)
		return err
	})
	return sum, err
}

// DecisionOverviewSQL counts the decisions in [$1, $2) by outcome, and the
// blocks whose fired rules include the entry named $3, which is how a
// blocklist block is saved. It also finds the 95th percentile scoring latency
// and the first decision time ($1 when there are none).
const DecisionOverviewSQL = `
SELECT
    count(*) FILTER (WHERE outcome = 'allow') AS allowed,
    count(*) FILTER (WHERE outcome = 'review') AS reviewed,
    count(*) FILTER (WHERE outcome = 'block') AS blocked,
    count(*) FILTER (WHERE outcome = 'block' AND fired_rules @> jsonb_build_array(jsonb_build_object('name', $3::text))) AS blocklist_blocked,
    count(*) AS total,
    count(scoring_latency_us) AS latency_samples,
    coalesce(percentile_disc(0.95) WITHIN GROUP (ORDER BY scoring_latency_us), 0)::bigint AS p95_latency_us,
    coalesce(min(decided_at), $1::timestamptz)::timestamptz AS first_decision_at
FROM decisions
WHERE decided_at >= $1::timestamptz
  AND decided_at < $2::timestamptz`

// overview is a DecisionOverviewSQL row.
type overview struct {
	models.OutcomeCounts
	blocklistBlocked int64
	latencySamples   int64
	p95LatencyUs     int64
	firstDecisionAt  time.Time
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

// RecentPipelineSQL counts the newest $3 decisions with a purchase ID above $1
// by outcome, the blocks whose fired rules include the entry named $2, and the
// decisions the model changed (see CountModelEffectsSQL). It also finds the
// largest purchase ID counted, $1 when there are none.
const RecentPipelineSQL = `
SELECT
    count(*) FILTER (WHERE d.outcome = 'allow') AS allowed,
    count(*) FILTER (WHERE d.outcome = 'review') AS reviewed,
    count(*) FILTER (WHERE d.outcome = 'block') AS blocked,
    count(*) FILTER (WHERE d.outcome = 'block' AND d.fired_rules @> jsonb_build_array(jsonb_build_object('name', $2::text))) AS blocklist_blocked,
    count(*) FILTER (WHERE d.outcome = 'review' AND d.rule_score < r.review_threshold) AS flagged,
    count(*) FILTER (WHERE d.outcome = 'allow' AND d.rule_score >= r.review_threshold) AS cleared,
    count(*) AS total,
    coalesce(max(d.purchase_id), $1::bigint) AS next_after
FROM (
    SELECT purchase_id, outcome, rule_score, ruleset_version, fired_rules
    FROM decisions
    WHERE purchase_id > $1::bigint
    ORDER BY purchase_id DESC
    LIMIT $3
) d
JOIN rulesets r ON r.version = d.ruleset_version`

// LatestDecisionIDSQL finds the largest purchase ID with a decision, 0 when
// there are none.
const LatestDecisionIDSQL = `SELECT coalesce(max(purchase_id), 0) FROM decisions`

// RecentPipeline counts the newest limit decisions after a purchase ID.
func (s *StatsStore) RecentPipeline(ctx context.Context, after int64, limit int) (models.RecentPipeline, error) {
	return readRecentPipeline(ctx, s.pool, after, limit)
}

func readRecentPipeline(ctx context.Context, conn platform.DBTX, after int64, limit int) (models.RecentPipeline, error) {
	var p models.RecentPipeline
	err := conn.QueryRow(ctx, RecentPipelineSQL, after, string(scoring.RuleBlocklist), int32(limit)).Scan(
		&p.Outcomes.Allow, &p.Outcomes.Review, &p.Outcomes.Block, &p.BlocklistBlocks,
		&p.Model.Flagged, &p.Model.Cleared, &p.Outcomes.Total, &p.NextAfter,
	)
	if err != nil {
		return models.RecentPipeline{}, fmt.Errorf("count recent decisions: %w", err)
	}
	return p, nil
}

// LatestDecisionID returns the largest purchase ID with a decision.
func (s *StatsStore) LatestDecisionID(ctx context.Context) (int64, error) {
	return readLatestDecisionID(ctx, s.pool)
}

func readLatestDecisionID(ctx context.Context, conn platform.DBTX) (int64, error) {
	var id int64
	if err := conn.QueryRow(ctx, LatestDecisionIDSQL).Scan(&id); err != nil {
		return 0, fmt.Errorf("read latest decision: %w", err)
	}
	return id, nil
}

func readSummary(ctx context.Context, conn platform.DBTX, window models.StatsWindow, maxRules int) (models.StatsSummary, error) {
	since, until := window.Since, window.Until
	var o overview
	err := conn.QueryRow(ctx, DecisionOverviewSQL, since, until, string(scoring.RuleBlocklist)).Scan(
		&o.Allow, &o.Review, &o.Block, &o.blocklistBlocked, &o.Total, &o.latencySamples, &o.p95LatencyUs, &o.firstDecisionAt,
	)
	if err != nil {
		return models.StatsSummary{}, fmt.Errorf("read decision overview: %w", err)
	}
	sum := models.StatsSummary{Outcomes: o.OutcomeCounts, BlocklistBlocks: o.blocklistBlocked, TopRules: []models.RuleCount{}}
	if o.latencySamples > 0 {
		sum.P95ScoringLatencyUs = &o.p95LatencyUs
	}
	if err := conn.QueryRow(ctx, BlockedValueSQL, since, until).Scan(&sum.BlockedValueMinor); err != nil {
		return models.StatsSummary{}, fmt.Errorf("sum blocked value: %w", err)
	}
	if sum.DecisionSeries, err = readDecisionSeries(ctx, conn, window, o); err != nil {
		return models.StatsSummary{}, fmt.Errorf("read decision buckets: %w", err)
	}

	rows, err := conn.Query(ctx, TopFiredRulesSQL, since, until, int32(maxRules))
	if err != nil {
		return models.StatsSummary{}, fmt.Errorf("count fired rules: %w", err)
	}
	sum.TopRules, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.RuleCount, error) {
		var r models.RuleCount
		return r, row.Scan(&r.Name, &r.Fired, &r.Points)
	})
	if err != nil {
		return models.StatsSummary{}, fmt.Errorf("count fired rules: %w", err)
	}

	if err := conn.QueryRow(ctx, CountReviewQueueSQL).Scan(&sum.ReviewQueue); err != nil {
		return models.StatsSummary{}, fmt.Errorf("count review queue: %w", err)
	}
	if err := conn.QueryRow(ctx, CountModelEffectsSQL, since, until).Scan(&sum.Model.Scored, &sum.Model.Flagged, &sum.Model.Cleared); err != nil {
		return models.StatsSummary{}, fmt.Errorf("count model effects: %w", err)
	}
	return sum, nil
}

// CountDecisionBucketsSQL counts decisions by outcome in buckets of width $1
// aligned to the Unix epoch. Buckets without decisions have no row.
const CountDecisionBucketsSQL = `
SELECT
    date_bin($1::interval, decided_at, TIMESTAMPTZ '1970-01-01 00:00:00+00')::timestamptz AS bucket_start,
    count(*) FILTER (WHERE outcome = 'allow') AS allowed,
    count(*) FILTER (WHERE outcome = 'review') AS reviewed,
    count(*) FILTER (WHERE outcome = 'block') AS blocked
FROM decisions
WHERE decided_at >= $2::timestamptz
  AND decided_at < $3::timestamptz
GROUP BY bucket_start
ORDER BY bucket_start`

func readDecisionSeries(ctx context.Context, conn platform.DBTX, window models.StatsWindow, o overview) (models.DecisionSeries, error) {
	width, start := window.BucketWidth, window.Since
	if width == 0 {
		start = o.firstDecisionAt
		width = time.Minute
		if o.Total > 0 {
			width = allTimeBucketWidth(start, window.Until)
		}
	}
	series := models.DecisionSeries{BucketSeconds: int64(width / time.Second), Buckets: []models.DecisionBucket{}}
	if o.Total == 0 {
		return series, nil
	}

	rows, err := conn.Query(ctx, CountDecisionBucketsSQL,
		pgtype.Interval{Microseconds: width.Microseconds(), Valid: true}, window.Since, window.Until)
	if err != nil {
		return models.DecisionSeries{}, err
	}
	counted, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.DecisionBucket, error) {
		var b models.DecisionBucket
		return b, row.Scan(&b.Start, &b.Allow, &b.Review, &b.Block)
	})
	if err != nil {
		return models.DecisionSeries{}, err
	}
	for at := bucketStart(start, width); at.Before(window.Until); at = at.Add(width) {
		bucket := models.DecisionBucket{Start: at}
		if len(counted) > 0 && counted[0].Start.Equal(at) {
			bucket.Allow, bucket.Review, bucket.Block = counted[0].Allow, counted[0].Review, counted[0].Block
			counted = counted[1:]
		}
		series.Buckets = append(series.Buckets, bucket)
	}
	return series, nil
}

const maxAllTimeBuckets = 48

func allTimeBucketWidth(first, until time.Time) time.Duration {
	const day = 24 * time.Hour
	last := until.Add(-time.Microsecond)
	fits := func(width time.Duration) bool {
		return bucketStart(last, width).Sub(bucketStart(first, width))/width < maxAllTimeBuckets
	}
	for _, width := range [...]time.Duration{time.Minute, 5 * time.Minute, time.Hour, 6 * time.Hour, day, 7 * day, 28 * day} {
		if fits(width) {
			return width
		}
	}
	// Start with the minimum whole-day width that could fit the span. Check
	// alignment too, since the two edge buckets can be partial.
	width := (last.Sub(first)/(maxAllTimeBuckets*day) + 1) * day
	for !fits(width) {
		width += day
	}
	return width
}

// bucketStart aligns with date_bin's Unix epoch origin and fixed-width stride.
func bucketStart(at time.Time, width time.Duration) time.Time {
	seconds := int64(width / time.Second)
	remainder := at.Unix() % seconds
	if remainder < 0 {
		remainder += seconds
	}
	return time.Unix(at.Unix()-remainder, 0).UTC()
}
