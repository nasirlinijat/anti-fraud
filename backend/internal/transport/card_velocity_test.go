package transport

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

func TestReviewCardVelocityUsesTheScoringSnapshot(t *testing.T) {
	tx := testTx(t)
	store := storage.NewPurchaseStore(tx)
	reviews := storage.NewReviewStore(tx)
	end := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	velocity := scoring.Ruleset{Version: 1, ReviewThreshold: 30, BlockThreshold: 80, Rules: map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleCardVelocity: {Enabled: true, Weight: 40, Limit: 1},
	}}
	scorer := score.NewService(fakeRulesets{rs: velocity}, storage.NewBlocklistStore(tx), store)
	scoringCase := velocityPurchase("velocity-case", end)
	saveVelocityFixture(t, store, velocityPurchase("velocity-first", end.Add(-45*time.Minute)))
	saveVelocityFixture(t, store, velocityPurchase("velocity-outside", end.Add(-time.Hour)))

	scored, err := scorer.Score(t.Context(), scoringCase, time.Now())
	if err != nil || scored.Result.Outcome != scoring.OutcomeReview {
		t.Fatalf("score = %+v, %v", scored, err)
	}
	id := velocityCaseID(t, tx, scoringCase.TransactionID)
	lateID := saveVelocityFixture(t, store, velocityPurchase("velocity-late", end.Add(-10*time.Minute)))
	// The fixture transaction uses a single database clock for all inserts.
	// Set a deterministic arrival time while leaving the purchase inside the rule window.
	if _, err := tx.Exec(t.Context(), "UPDATE purchases SET received_at = now() + interval '1 second' WHERE id = $1", lateID); err != nil {
		t.Fatal(err)
	}

	retry, err := scorer.Score(t.Context(), scoringCase, time.Now())
	if err != nil || len(retry.Result.FiredRules) != 1 || retry.Result.FiredRules[0].Message != "Card used 1 time in 60 minutes (limit 1)" {
		t.Fatalf("retry = %+v, %v", retry, err)
	}
	response := requestReviewRead(t, reviews, fmt.Sprintf("/reviews/%d", id), queueTestKey)
	if response.Code != http.StatusOK {
		t.Fatalf("review status = %d: %s", response.Code, response.Body)
	}
	got := decodeJSON[caseResponse](t, response).CardVelocity
	if got == nil || !got.RecordsAvailable || got.Count != 1 || got.Limit != 1 ||
		!got.WindowStart.Equal(end.Add(-time.Hour)) || !got.WindowEnd.Equal(end) || len(got.Purchases) != 2 {
		t.Fatalf("evidence = %+v", got)
	}
	first, late := got.Purchases[0], got.Purchases[1]
	if first.TransactionID != "velocity-first" || !first.Contributed || first.RecordedAfterDecision ||
		late.TransactionID != "velocity-late" || late.Contributed || !late.RecordedAfterDecision {
		t.Fatalf("purchases = %+v", got.Purchases)
	}
}

func velocityPurchase(transactionID string, at time.Time) models.Purchase {
	return models.Purchase{TransactionID: transactionID, PlayerID: "velocity-player", CardToken: "velocity-card", AmountMinor: 499, EventTime: at}
}

func saveVelocityFixture(t *testing.T, store *storage.PurchaseStore, p models.Purchase) int64 {
	t.Helper()
	id, err := store.Save(t.Context(), p, models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func velocityCaseID(t *testing.T, tx pgx.Tx, transactionID string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(t.Context(), "SELECT id FROM purchases WHERE transaction_id = $1", transactionID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
