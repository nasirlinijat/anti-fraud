package storage

import (
	"context"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func TestExportDecisionsNewestFirstWithReviewResults(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	high := scoring.FiredRule{Name: scoring.RuleHighAmount, Points: 30}
	velocity := scoring.FiredRule{Name: scoring.RuleCardVelocity, Points: 40}

	saveStatsDecision(t, tx, "x-old", scoring.OutcomeAllow, 0, nil, nil, 3*time.Hour)
	approved := saveStatsDecision(t, tx, "x-approved", scoring.OutcomeReview, 70, []scoring.FiredRule{velocity, high}, ptr(0.4), 2*time.Minute)
	saveStatsDecision(t, tx, "x-open", scoring.OutcomeReview, 50, []scoring.FiredRule{high}, nil, time.Minute)
	saveStatsDecision(t, tx, "x-outside", scoring.OutcomeBlock, 90, nil, nil, 30*time.Hour)
	resolvedAt := statsNow.Add(-time.Minute)
	if _, err := tx.Exec(ctx, `INSERT INTO review_resolutions (purchase_id, decision, analyst_id, resolved_at) VALUES ($1, 'approve', 'reviewer-one', $2)`, approved, resolvedAt); err != nil {
		t.Fatal(err)
	}

	window := models.StatsWindow{Since: statsNow.Add(-24 * time.Hour), Until: statsNow}
	total, err := countDecisions(ctx, tx, window)
	if err != nil {
		t.Fatal(err)
	}
	var got []models.DecisionExport
	if err := exportDecisions(ctx, tx, window, 10, func(d models.DecisionExport) error { got = append(got, d); return nil }); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, d := range got {
		if d.TransactionID[0] == 'x' {
			ids = append(ids, d.TransactionID)
		}
	}
	if total < 3 || len(ids) != 3 || ids[0] != "x-open" || ids[1] != "x-approved" || ids[2] != "x-old" {
		t.Fatalf("total %d, exported %v; want x-open, x-approved, x-old in the window, newest first", total, ids)
	}

	var open, done, allowed models.DecisionExport
	for _, d := range got {
		switch d.TransactionID {
		case "x-open":
			open = d
		case "x-approved":
			done = d
		case "x-old":
			allowed = d
		}
	}
	if open.Review != "open" || open.FiredRules != "high_amount" || open.ModelScore != nil || open.ReviewedBy != nil {
		t.Fatalf("open review = %+v", open)
	}
	if done.Review != "approved" || done.FiredRules != "card_velocity;high_amount" || done.ModelScore == nil || *done.ModelScore != 0.4 ||
		done.ReviewedBy == nil || *done.ReviewedBy != "reviewer-one" || done.ReviewedAt == nil || !done.ReviewedAt.Equal(resolvedAt) {
		t.Fatalf("approved review = %+v", done)
	}
	if allowed.Review != "" || allowed.Outcome != "allow" || allowed.AmountMinor != 1000 || allowed.PlayerID != "player-x-old" || allowed.RulesetVersion != 1 {
		t.Fatalf("allowed decision = %+v", allowed)
	}
}

func TestExportDecisionsStopsAtTheLimit(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	for _, id := range []string{"y-1", "y-2", "y-3"} {
		saveStatsDecision(t, tx, id, scoring.OutcomeAllow, 0, nil, nil, time.Minute)
	}
	window := models.StatsWindow{Since: statsNow.Add(-time.Hour), Until: statsNow}
	n := 0
	if err := exportDecisions(ctx, tx, window, 2, func(models.DecisionExport) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("exported %d rows, want the limit of 2", n)
	}
}
