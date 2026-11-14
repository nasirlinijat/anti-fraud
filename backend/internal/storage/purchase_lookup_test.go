package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func TestSearchPurchasesFindsByTransactionPlayerOrCard(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	open := saveStatsDecision(t, tx, "s-open", scoring.OutcomeReview, 50, nil, nil, time.Minute)
	approved := saveStatsDecision(t, tx, "s-approved", scoring.OutcomeReview, 50, nil, nil, time.Minute)
	saveStatsDecision(t, tx, "s-allowed", scoring.OutcomeAllow, 0, nil, nil, time.Minute)
	if _, err := tx.Exec(ctx, `INSERT INTO review_resolutions (purchase_id, decision, analyst_id) VALUES ($1, 'approve', 'reviewer-one')`, approved); err != nil {
		t.Fatal(err)
	}
	store := NewReviewStore(tx)

	cases := []struct {
		query, transaction, matchedBy, outcome, review string
	}{
		{"s-open", "s-open", "transaction", "review", "open"},
		{"player-s-approved", "s-approved", "player", "review", "approved"},
		{"card-s-allowed", "s-allowed", "card", "allow", ""},
	}
	for _, c := range cases {
		got, err := store.SearchPurchases(ctx, c.query, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].TransactionID != c.transaction || got[0].MatchedBy != c.matchedBy || got[0].Outcome != c.outcome || got[0].Review != c.review {
			t.Fatalf("search %q = %+v", c.query, got)
		}
	}
	if got, err := store.SearchPurchases(ctx, "s-op", 20); err != nil || len(got) != 0 {
		t.Fatalf("a partial value must not match: %+v, %v", got, err)
	}
	if got, err := store.SearchPurchases(ctx, "s-open", 20); err != nil || got[0].PurchaseID != open || got[0].AmountMinor != 1000 || got[0].PlayerID != "player-s-open" {
		t.Fatalf("match fields = %+v, %v", got, err)
	}
}

func TestSearchPurchasesNewestFirstUpToTheLimit(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	for i, id := range []string{"n-1", "n-2", "n-3"} {
		purchaseID := saveStatsDecision(t, tx, id, scoring.OutcomeAllow, 0, nil, nil, time.Minute)
		if _, err := tx.Exec(ctx, `UPDATE purchases SET player_id = 'shared-player', event_time = $1 WHERE id = $2`,
			statsNow.Add(time.Duration(i)*time.Minute), purchaseID); err != nil {
			t.Fatal(err)
		}
	}
	got, err := NewReviewStore(tx).SearchPurchases(ctx, "shared-player", 2)
	if err != nil || len(got) != 2 || got[0].TransactionID != "n-3" || got[1].TransactionID != "n-2" {
		t.Fatalf("got %+v, %v; want n-3, n-2", got, err)
	}
}

func TestPurchaseCaseShowsAnyOutcomeAndItsReview(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	allowed := saveStatsDecision(t, tx, "p-allowed", scoring.OutcomeAllow, 10, []scoring.FiredRule{{Name: scoring.RuleHighAmount, Points: 10}}, nil, time.Minute)
	rejected := saveStatsDecision(t, tx, "p-rejected", scoring.OutcomeReview, 60, nil, nil, time.Minute)
	resolvedAt := statsNow.Add(-time.Minute)
	if _, err := tx.Exec(ctx, `INSERT INTO review_resolutions (purchase_id, decision, analyst_id, resolved_at) VALUES ($1, 'reject', 'reviewer-two', $2)`, rejected, resolvedAt); err != nil {
		t.Fatal(err)
	}
	store := NewReviewStore(tx)

	got, err := store.PurchaseCase(ctx, allowed)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "allow" || got.Resolution != nil || got.TransactionID != "p-allowed" || len(got.FiredRules) != 1 || got.PlayerPurchases == nil {
		t.Fatalf("allowed purchase = %+v", got)
	}

	got, err = store.PurchaseCase(ctx, rejected)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "review" || got.Resolution == nil || got.Resolution.Decision != "reject" || got.Resolution.AnalystID != "reviewer-two" || !got.Resolution.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("rejected review = %+v (resolution %+v)", got, got.Resolution)
	}

	if _, err := store.PurchaseCase(ctx, -1); !errors.Is(err, models.ErrPurchaseNotFound) {
		t.Fatalf("unknown purchase: %v", err)
	}
}
