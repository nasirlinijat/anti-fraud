package stats_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/stats"
)

func testTx(t *testing.T) pgx.Tx {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(ctx) })

	// Start from no decisions inside the transaction; the rollback restores them.
	for _, table := range []string{"review_resolutions", "decisions", "purchases", "feature_counts"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("empty %s: %v", table, err)
		}
	}
	return tx
}

func ptr[T any](v T) *T { return &v }

// save stores a decision with the given outcome, rule score and fired rules,
// decided the given time ago.
func save(t *testing.T, tx pgx.Tx, id string, outcome scoring.Outcome, ruleScore int, rules []scoring.FiredRule, modelScore *float64, ago time.Duration) {
	t.Helper()
	ctx := context.Background()
	p := score.Purchase{
		TransactionID: id,
		EventTime:     time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		AmountMinor:   1000,
		PlayerID:      "player-" + id,
		CardToken:     "card-" + id,
	}
	d := score.Decision{
		RulesetVersion: 1,
		Result:         scoring.Result{Outcome: outcome, Score: ruleScore, FiredRules: rules},
		ModelScore:     modelScore,
	}
	purchaseID, err := score.NewStore(tx).Save(ctx, p, d)
	if err != nil {
		t.Fatalf("Save %s: %v", id, err)
	}
	if _, err := tx.Exec(ctx, "UPDATE decisions SET decided_at = now() - $1::interval WHERE purchase_id = $2", ago.String(), purchaseID); err != nil {
		t.Fatalf("set decided_at: %v", err)
	}
}

func TestSummary(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	high := scoring.FiredRule{Name: scoring.RuleHighAmount, Points: 30}
	newAccount := scoring.FiredRule{Name: scoring.RuleNewAccount, Points: 20}
	mismatch := scoring.FiredRule{Name: scoring.RuleCountryMismatch, Points: 25}
	velocity := scoring.FiredRule{Name: scoring.RuleCardVelocity, Points: 40}

	// The default ruleset reviews at 50 and blocks at 80.
	save(t, tx, "a", scoring.OutcomeAllow, 0, nil, ptr(0.01), time.Minute)
	save(t, tx, "b", scoring.OutcomeAllow, 30, []scoring.FiredRule{high}, ptr(0.02), time.Minute)
	save(t, tx, "c", scoring.OutcomeReview, 50, []scoring.FiredRule{high, newAccount}, ptr(0.2), time.Minute)
	save(t, tx, "d", scoring.OutcomeReview, 0, nil, ptr(0.9), time.Minute)                                           // flagged by the model
	save(t, tx, "e", scoring.OutcomeAllow, 55, []scoring.FiredRule{high, mismatch}, ptr(0.001), time.Minute)         // cleared by the model
	save(t, tx, "f", scoring.OutcomeBlock, 95, []scoring.FiredRule{velocity, high, mismatch}, ptr(0.5), time.Minute) // blocked by the rules
	save(t, tx, "old", scoring.OutcomeReview, 50, []scoring.FiredRule{high, newAccount}, nil, 3*time.Hour)

	store := stats.NewStore(tx)
	got, err := store.Summary(ctx, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	want := stats.Summary{
		Outcomes: stats.Outcomes{Allow: 3, Review: 2, Block: 1, Total: 6},
		TopRules: []stats.RuleCount{
			{Name: "high_amount", Fired: 4, Points: 120},
			{Name: "country_mismatch", Fired: 2, Points: 50},
			{Name: "card_velocity", Fired: 1, Points: 40},
			{Name: "new_account", Fired: 1, Points: 20},
		},
		ReviewQueue: 3,
		Model:       stats.ModelEffects{Scored: 6, Flagged: 1, Cleared: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("last hour summary =\n%+v\nwant\n%+v", got, want)
	}

	all, err := store.Summary(ctx, time.Unix(0, 0), 2)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if all.Outcomes.Total != 7 || all.Outcomes.Review != 3 || all.Model.Scored != 6 {
		t.Errorf("all time outcomes %+v, model %+v; want 7 decisions, 3 reviews, 6 scored", all.Outcomes, all.Model)
	}
	if len(all.TopRules) != 2 || all.TopRules[0].Name != "high_amount" || all.TopRules[0].Fired != 5 {
		t.Errorf("all time top rules = %+v, want 2 rules led by high_amount fired 5 times", all.TopRules)
	}
}

func TestSummaryWithoutDecisions(t *testing.T) {
	tx := testTx(t)
	got, err := stats.NewStore(tx).Summary(context.Background(), time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	want := stats.Summary{TopRules: []stats.RuleCount{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty summary = %+v, want %+v", got, want)
	}
}
