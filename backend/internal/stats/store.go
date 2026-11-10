package stats

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
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
		sum, err = readSummary(ctx, db.New(tx), window, maxRules)
		return err
	})
	return sum, err
}

func readSummary(ctx context.Context, q *db.Queries, window Window, maxRules int) (Summary, error) {
	since := pgtype.Timestamptz{Time: window.Since, Valid: true}
	until := pgtype.Timestamptz{Time: window.Until, Valid: true}
	overview, err := q.DecisionOverview(ctx, db.DecisionOverviewParams{Since: since, Until: until})
	if err != nil {
		return Summary{}, fmt.Errorf("read decision overview: %w", err)
	}
	sum := Summary{
		Outcomes: Outcomes{Allow: overview.Allowed, Review: overview.Reviewed, Block: overview.Blocked, Total: overview.Total},
		TopRules: []RuleCount{},
	}
	if overview.LatencySamples > 0 {
		sum.P95ScoringLatencyUs = &overview.P95LatencyUs
	}
	if sum.BlockedValueMinor, err = q.BlockedValue(ctx, db.BlockedValueParams{Since: since, Until: until}); err != nil {
		return Summary{}, fmt.Errorf("sum blocked value: %w", err)
	}
	if sum.DecisionSeries, err = readDecisionSeries(ctx, q, window, overview); err != nil {
		return Summary{}, fmt.Errorf("read decision buckets: %w", err)
	}

	rules, err := q.TopFiredRules(ctx, db.TopFiredRulesParams{Since: since, Until: until, MaxRules: int32(maxRules)})
	if err != nil {
		return Summary{}, fmt.Errorf("count fired rules: %w", err)
	}
	for _, r := range rules {
		sum.TopRules = append(sum.TopRules, RuleCount{Name: r.Name, Fired: r.Fired, Points: r.Points})
	}

	if sum.ReviewQueue, err = q.CountReviewQueue(ctx); err != nil {
		return Summary{}, fmt.Errorf("count review queue: %w", err)
	}

	model, err := q.CountModelEffects(ctx, db.CountModelEffectsParams{Since: since, Until: until})
	if err != nil {
		return Summary{}, fmt.Errorf("count model effects: %w", err)
	}
	sum.Model = ModelEffects{Scored: model.Scored, Flagged: model.Flagged, Cleared: model.Cleared}
	return sum, nil
}
