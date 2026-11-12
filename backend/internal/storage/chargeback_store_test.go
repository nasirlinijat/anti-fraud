package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func TestChargebackStoreRoundTrip(t *testing.T) {
	tx := testTx(t)
	ctx := t.Context()
	_, err := NewPurchaseStore(tx).Save(ctx, minimalPurchase("cb-store-1"),
		models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}, ModelInputs: map[string]float64{}})
	if err != nil {
		t.Fatalf("save purchase: %v", err)
	}
	store := NewChargebackStore(tx)
	purchase, err := store.Purchase(ctx, "cb-store-1")
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	if !purchase.EventTime.Equal(time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)) || purchase.AmountMinor != 1999 {
		t.Fatalf("purchase = %+v, want the saved event time and amount", purchase)
	}
	c := models.Chargeback{
		ChargebackID: "cb-store-1", TransactionID: "cb-store-1", Reason: models.ChargebackNotRecognised,
		AmountMinor: 1500, ReceivedAt: time.Date(2026, 12, 1, 8, 30, 0, 123000, time.UTC),
	}

	first, created, err := store.Insert(ctx, purchase.ID, c)
	if err != nil || !created {
		t.Fatalf("first Insert = %v, created %v", err, created)
	}
	again, created, err := store.Insert(ctx, purchase.ID, models.Chargeback{ChargebackID: "cb-store-1", TransactionID: "cb-store-1", Reason: models.ChargebackOther, AmountMinor: 1, ReceivedAt: c.ReceivedAt})

	if err != nil || created {
		t.Fatalf("second Insert = %v, created %v; want the stored chargeback", err, created)
	}
	if again.Reason != models.ChargebackNotRecognised || again.AmountMinor != 1500 || !again.ReceivedAt.Equal(c.ReceivedAt) ||
		again.TransactionID != "cb-store-1" || !again.RecordedAt.Equal(first.RecordedAt) {
		t.Errorf("stored = %+v, want the first chargeback %+v", again, first)
	}
}

func TestChargebackStorePurchaseNotFound(t *testing.T) {
	_, err := NewChargebackStore(testTx(t)).Purchase(t.Context(), "cb-store-missing")

	if !errors.Is(err, models.ErrPurchaseNotFound) {
		t.Fatalf("err = %v, want ErrPurchaseNotFound", err)
	}
}
