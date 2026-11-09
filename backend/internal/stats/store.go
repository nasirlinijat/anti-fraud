package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
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

// Summary is the dashboard data for decisions made since a time.
type Summary struct {
	Outcomes    Outcomes     `json:"outcomes"`
	TopRules    []RuleCount  `json:"top_rules"`
	ReviewQueue int64        `json:"review_queue"`
	Model       ModelEffects `json:"model"`
}

// Store reads decision statistics.
type Store struct {
	conn db.DBTX
}

func NewStore(conn db.DBTX) *Store {
	return &Store{conn: conn}
}

// Summary counts the decisions made at or after since. The review queue size is
// the current one, whatever the period.
func (s *Store) Summary(ctx context.Context, since time.Time, maxRules int) (Summary, error) {
	q := db.New(s.conn)
	at := pgtype.Timestamptz{Time: since.UTC(), Valid: true}
	sum := Summary{TopRules: []RuleCount{}}

	outcomes, err := q.CountOutcomes(ctx, at)
	if err != nil {
		return Summary{}, fmt.Errorf("count outcomes: %w", err)
	}
	for _, o := range outcomes {
		switch o.Outcome {
		case "allow":
			sum.Outcomes.Allow = o.Decisions
		case "review":
			sum.Outcomes.Review = o.Decisions
		case "block":
			sum.Outcomes.Block = o.Decisions
		}
		sum.Outcomes.Total += o.Decisions
	}

	rules, err := q.TopFiredRules(ctx, db.TopFiredRulesParams{Since: at, MaxRules: int32(maxRules)})
	if err != nil {
		return Summary{}, fmt.Errorf("count fired rules: %w", err)
	}
	for _, r := range rules {
		sum.TopRules = append(sum.TopRules, RuleCount{Name: r.Name, Fired: r.Fired, Points: r.Points})
	}

	if sum.ReviewQueue, err = q.CountReviewQueue(ctx); err != nil {
		return Summary{}, fmt.Errorf("count review queue: %w", err)
	}

	model, err := q.CountModelEffects(ctx, at)
	if err != nil {
		return Summary{}, fmt.Errorf("count model effects: %w", err)
	}
	sum.Model = ModelEffects{Scored: model.Scored, Flagged: model.Flagged, Cleared: model.Cleared}
	return sum, nil
}
