package storage

import (
	"context"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func TestRecentDecisionsNewestFirstWithTheirRules(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	high := scoring.FiredRule{Name: scoring.RuleHighAmount, Points: 30}
	saveStatsDecision(t, tx, "r-old", scoring.OutcomeAllow, 0, nil, nil, 2*time.Minute)
	newest := saveStatsDecision(t, tx, "r-new", scoring.OutcomeReview, 30, []scoring.FiredRule{high}, ptr(0.4), time.Minute)
	for id, ahead := range map[string]string{"r-new": "1 day 2 seconds", "r-old": "1 day"} {
		if _, err := tx.Exec(ctx, `UPDATE decisions SET decided_at = now() + $2::interval WHERE purchase_id = (SELECT id FROM purchases WHERE transaction_id = $1)`, id, ahead); err != nil {
			t.Fatal(err)
		}
	}

	got, err := NewRulesetStore(tx).RecentDecisions(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TransactionID != "r-new" || got[1].TransactionID != "r-old" {
		t.Fatalf("got %+v; want r-new then r-old", got)
	}
	d := got[0]
	if d.PurchaseID != newest || d.Outcome != "review" || d.RuleScore != 30 || len(d.FiredRules) != 1 || d.FiredRules[0].Name != scoring.RuleHighAmount ||
		d.ModelScore == nil || *d.ModelScore != 0.4 || d.RulesetVersion != 1 {
		t.Fatalf("newest decision = %+v", d)
	}
}
