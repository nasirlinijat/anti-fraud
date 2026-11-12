package labels_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

// labelledPurchase commits a purchase with its decision, analyst resolution
// ("" for none) and simulator label, so the evaluation role can read it.
func labelledPurchase(t *testing.T, owner *pgxpool.Pool, id, outcome, resolution string) {
	t.Helper()
	ctx := t.Context()
	_, err := owner.Exec(ctx, `WITH p AS (INSERT INTO purchases(transaction_id,event_time,amount_minor,player_id,card_token) VALUES($1,'2026-11-10T10:00:00Z',700,$1,$1) RETURNING id) INSERT INTO decisions(purchase_id,ruleset_version,outcome,rule_score,fired_rules,model_inputs) SELECT id,(SELECT version FROM rulesets WHERE is_active),$2,0,'[]','{}' FROM p`, id, outcome)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = owner.Exec(ctx, `DELETE FROM purchase_labels WHERE transaction_id=$1`, id)
		_, _ = owner.Exec(ctx, `DELETE FROM review_resolutions WHERE purchase_id IN (SELECT id FROM purchases WHERE transaction_id=$1)`, id)
		_, _ = owner.Exec(ctx, `DELETE FROM decisions WHERE purchase_id IN (SELECT id FROM purchases WHERE transaction_id=$1)`, id)
		_, _ = owner.Exec(ctx, `DELETE FROM purchases WHERE transaction_id=$1`, id)
	})
	if resolution != "" {
		if _, err := owner.Exec(ctx, `INSERT INTO review_resolutions(purchase_id,decision,analyst_id) SELECT id,$2,'analyst' FROM purchases WHERE transaction_id=$1`, id, resolution); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := owner.Exec(ctx, `INSERT INTO purchase_labels(transaction_id,run_id,source,scenario,is_fraud) VALUES($1,'settled-run','simulator','stolen_card',true)`, id); err != nil {
		t.Fatal(err)
	}
}

func TestSettledPurchasesListsOnlyChargedPurchasesOfTheRun(t *testing.T) {
	owner, evaluation := setup(t)
	labelledPurchase(t, owner, "settled-allow", "allow", "")
	labelledPurchase(t, owner, "settled-approved", "review", "approve")
	labelledPurchase(t, owner, "settled-rejected", "review", "reject")
	labelledPurchase(t, owner, "settled-undecided", "review", "")
	labelledPurchase(t, owner, "settled-blocked", "block", "")

	got, err := labels.SettledPurchases(t.Context(), evaluation, "settled-run")

	if err != nil {
		t.Fatalf("SettledPurchases with the evaluation role: %v", err)
	}
	at := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	want := []labels.SettledPurchase{
		{TransactionID: "settled-allow", EventTime: at, AmountMinor: 700, Pattern: simulator.PatternStolenCard},
		{TransactionID: "settled-approved", EventTime: at, AmountMinor: 700, Pattern: simulator.PatternStolenCard},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("settled = %+v, want %+v", got, want)
	}
}
