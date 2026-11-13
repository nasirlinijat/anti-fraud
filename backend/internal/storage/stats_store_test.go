package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// save stores a decision with the given outcome, rule score and fired rules,
// decided the given time ago.

var statsNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func saveStatsDecision(t *testing.T, tx pgx.Tx, id string, outcome scoring.Outcome, ruleScore int, rules []scoring.FiredRule, modelScore *float64, ago time.Duration) int64 {
	t.Helper()
	ctx := context.Background()
	p := models.Purchase{
		TransactionID: id,
		EventTime:     time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		AmountMinor:   1000,
		PlayerID:      "player-" + id,
		CardToken:     "card-" + id,
	}
	d := models.Decision{
		RulesetVersion: 1,
		Result:         scoring.Result{Outcome: outcome, Score: ruleScore, FiredRules: rules},
		ModelScore:     modelScore,
	}
	purchaseID, err := NewPurchaseStore(tx).Save(ctx, p, d)
	if err != nil {
		t.Fatalf("Save %s: %v", id, err)
	}
	if _, err := tx.Exec(ctx, "UPDATE decisions SET decided_at = $1 WHERE purchase_id = $2", statsNow.Add(-ago), purchaseID); err != nil {
		t.Fatalf("set decided_at: %v", err)
	}
	return purchaseID
}

func TestSummary(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	high := scoring.FiredRule{Name: scoring.RuleHighAmount, Points: 30}
	newAccount := scoring.FiredRule{Name: scoring.RuleNewAccount, Points: 20}
	mismatch := scoring.FiredRule{Name: scoring.RuleCountryMismatch, Points: 25}
	velocity := scoring.FiredRule{Name: scoring.RuleCardVelocity, Points: 40}
	blocklisted := scoring.BlocklistMatch("card")

	// The default ruleset reviews at 50 and blocks at 80.
	saveStatsDecision(t, tx, "a", scoring.OutcomeAllow, 0, nil, ptr(0.01), time.Minute)
	saveStatsDecision(t, tx, "b", scoring.OutcomeAllow, 30, []scoring.FiredRule{high}, ptr(0.02), time.Minute)
	saveStatsDecision(t, tx, "c", scoring.OutcomeReview, 50, []scoring.FiredRule{high, newAccount}, ptr(0.2), time.Minute)
	saveStatsDecision(t, tx, "d", scoring.OutcomeReview, 0, nil, ptr(0.9), time.Minute)                                           // flagged by the model
	saveStatsDecision(t, tx, "e", scoring.OutcomeAllow, 55, []scoring.FiredRule{high, mismatch}, ptr(0.001), time.Minute)         // cleared by the model
	saveStatsDecision(t, tx, "f", scoring.OutcomeBlock, 95, []scoring.FiredRule{velocity, high, mismatch}, ptr(0.5), time.Minute) // blocked by the rules
	saveStatsDecision(t, tx, "g", scoring.OutcomeBlock, 0, []scoring.FiredRule{blocklisted}, ptr(0.4), time.Minute)               // blocked by the blocklist, still model scored
	saveStatsDecision(t, tx, "old", scoring.OutcomeReview, 50, []scoring.FiredRule{high, newAccount}, nil, 3*time.Hour)
	saveStatsDecision(t, tx, "old-blocklist", scoring.OutcomeBlock, 0, []scoring.FiredRule{blocklisted}, nil, 3*time.Hour)

	window := models.StatsWindow{Since: statsNow.Add(-time.Hour), Until: statsNow, BucketWidth: 5 * time.Minute}
	got, err := readSummary(ctx, tx, window, 10)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	want := models.StatsSummary{
		Outcomes:        models.OutcomeCounts{Allow: 3, Review: 2, Block: 2, Total: 7},
		BlocklistBlocks: 1,
		TopRules: []models.RuleCount{
			{Name: "high_amount", Fired: 4, Points: 120},
			{Name: "country_mismatch", Fired: 2, Points: 50},
			{Name: "blocklist", Fired: 1, Points: 0},
			{Name: "card_velocity", Fired: 1, Points: 40},
			{Name: "new_account", Fired: 1, Points: 20},
		},
		ReviewQueue:         3,
		Model:               models.ModelEffects{Scored: 7, Flagged: 1, Cleared: 1},
		P95ScoringLatencyUs: ptr(int64(0)),
		BlockedValueMinor:   "2000",
		DecisionSeries:      models.DecisionSeries{BucketSeconds: 300, Buckets: make([]models.DecisionBucket, 12)},
	}
	for i := range want.DecisionSeries.Buckets {
		want.DecisionSeries.Buckets[i].Start = window.Since.Add(time.Duration(i) * window.BucketWidth)
	}
	want.DecisionSeries.Buckets[11] = models.DecisionBucket{Start: statsNow.Add(-5 * time.Minute), Allow: 3, Review: 2, Block: 2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("last hour summary =\n%+v\nwant\n%+v", got, want)
	}

	all, err := readSummary(ctx, tx, models.StatsWindow{Since: time.Unix(0, 0).UTC(), Until: statsNow}, 2)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if all.Outcomes.Total != 9 || all.Outcomes.Review != 3 || all.Model.Scored != 7 {
		t.Errorf("all time outcomes %+v, model %+v; want 9 decisions, 3 reviews, 7 scored", all.Outcomes, all.Model)
	}
	if all.Outcomes.Block != 3 || all.BlocklistBlocks != 2 {
		t.Errorf("all time blocks %d, blocklist blocks %d; want 3 and 2", all.Outcomes.Block, all.BlocklistBlocks)
	}
	if len(all.TopRules) != 2 || all.TopRules[0].Name != "high_amount" || all.TopRules[0].Fired != 5 {
		t.Errorf("all time top rules = %+v, want 2 rules led by high_amount fired 5 times", all.TopRules)
	}
	if all.DecisionSeries.BucketSeconds != 300 || len(all.DecisionSeries.Buckets) != 36 ||
		all.DecisionSeries.Buckets[0].Review != 1 || all.DecisionSeries.Buckets[35].Allow != 3 {
		t.Errorf("unexpected all-time series: %+v", all.DecisionSeries)
	}
}

func TestSummaryWithoutDecisions(t *testing.T) {
	tx := testTx(t)
	got, err := readSummary(t.Context(), tx, models.StatsWindow{Since: time.Unix(0, 0).UTC(), Until: statsNow}, 10)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	want := models.StatsSummary{
		TopRules: []models.RuleCount{}, BlockedValueMinor: "0",
		DecisionSeries: models.DecisionSeries{BucketSeconds: 60, Buckets: []models.DecisionBucket{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty summary = %+v, want %+v", got, want)
	}
}

func TestDecisionMetricsUseTheSameWindow(t *testing.T) {
	tx := testTx(t)
	ctx := t.Context()
	if _, err := tx.Exec(ctx, "SET LOCAL TIME ZONE 'America/New_York'"); err != nil {
		t.Fatal(err)
	}
	window := models.StatsWindow{Since: statsNow.Add(-14*time.Minute - 30*time.Second), Until: statsNow.Add(30 * time.Second), BucketWidth: time.Minute}
	old := saveStatsDecision(t, tx, "before-window", scoring.OutcomeBlock, 80, nil, nil, statsNow.Sub(window.Since)+time.Microsecond)
	first := saveStatsDecision(t, tx, "at-lower-bound", scoring.OutcomeBlock, 80, nil, nil, statsNow.Sub(window.Since))
	last := saveStatsDecision(t, tx, "at-last-microsecond", scoring.OutcomeBlock, 80, nil, nil, statsNow.Sub(window.Until)+time.Microsecond)
	end := saveStatsDecision(t, tx, "at-upper-bound", scoring.OutcomeBlock, 80, nil, nil, statsNow.Sub(window.Until))
	rejected := saveStatsDecision(t, tx, "analyst-rejection", scoring.OutcomeReview, 50, nil, nil, 12*time.Minute)
	if _, err := tx.Exec(ctx, `INSERT INTO review_resolutions (purchase_id, decision, analyst_id) VALUES ($1, 'reject', 'test')`, rejected); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE decisions SET scoring_latency_us = NULL WHERE purchase_id = ANY($1::bigint[])`, []int64{old, first, last, end, rejected}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE purchases SET amount_minor = 9223372036854775807 WHERE id = ANY($1::bigint[])`, []int64{first, last}); err != nil {
		t.Fatal(err)
	}
	legacyOnly, err := readSummary(ctx, tx, window, 10)
	if err != nil || legacyOnly.P95ScoringLatencyUs != nil {
		t.Fatalf("legacy-only p95 = %v, error = %v", legacyOnly.P95ScoringLatencyUs, err)
	}

	for ms := 1; ms <= 20; ms++ {
		id := saveStatsDecision(t, tx, fmt.Sprintf("latency-%d", ms), scoring.OutcomeAllow, 0, nil, nil, 12*time.Minute)
		if _, err := tx.Exec(ctx, "UPDATE decisions SET scoring_latency_us = $1 WHERE purchase_id = $2", ms*1000, id); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := readSummary(ctx, tx, window, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sum.P95ScoringLatencyUs == nil || *sum.P95ScoringLatencyUs != 19000 || sum.BlockedValueMinor != "18446744073709551614" {
		t.Fatalf("unexpected p95 or blocked value: %+v", sum)
	}
	if sum.Outcomes != (models.OutcomeCounts{Allow: 20, Review: 1, Block: 2, Total: 23}) || sum.ReviewQueue != 0 {
		t.Fatalf("unexpected outcomes or queue: %+v", sum)
	}
	series := sum.DecisionSeries
	if series.BucketSeconds != 60 || len(series.Buckets) != 16 {
		t.Fatalf("unexpected bucket size or count: %+v", series)
	}
	for i, got := range series.Buckets {
		want := models.DecisionBucket{Start: statsNow.Add(time.Duration(i-15) * time.Minute)}
		switch i {
		case 0, 15:
			want.Block = 1
		case 3:
			want.Allow, want.Review = 20, 1
		}
		if got != want {
			t.Errorf("bucket %d = %+v, want %+v", i, got, want)
		}
	}
}

func TestAllTimeBucketWidth(t *testing.T) {
	for _, test := range []struct{ span, want time.Duration }{
		{48 * time.Minute, time.Minute},
		{48*time.Minute + time.Microsecond, 5 * time.Minute},
		{10 * 24 * time.Hour, 6 * time.Hour},
		{200 * 24 * time.Hour, 7 * 24 * time.Hour},
		{2000 * 24 * time.Hour, 42 * 24 * time.Hour},
	} {
		// An epoch-aligned start makes the expected number of buckets explicit.
		start := time.Unix(0, 0).UTC()
		if got := allTimeBucketWidth(start, start.Add(test.span)); got != test.want {
			t.Errorf("%v span width = %v, want %v", test.span, got, test.want)
		}
	}
}
