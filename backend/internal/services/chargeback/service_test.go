package chargeback_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/chargeback"
)

var (
	now       = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	purchased = models.ChargedPurchase{ID: 7, EventTime: now.Add(-30 * 24 * time.Hour), AmountMinor: 5000}
)

// fakeStore keeps recorded chargebacks in memory and knows one purchase, txn-1.
type fakeStore struct {
	recorded map[string]models.StoredChargeback
}

func (f *fakeStore) Purchase(_ context.Context, transactionID string) (models.ChargedPurchase, error) {
	if transactionID != "txn-1" {
		return models.ChargedPurchase{}, models.ErrPurchaseNotFound
	}
	return purchased, nil
}

func (f *fakeStore) Insert(_ context.Context, _ int64, c models.Chargeback) (models.StoredChargeback, bool, error) {
	if existing, ok := f.recorded[c.ChargebackID]; ok {
		return existing, false, nil
	}
	stored := models.StoredChargeback{Chargeback: c, RecordedAt: now}
	f.recorded[c.ChargebackID] = stored
	return stored, true, nil
}

func newService() *chargeback.Service {
	return chargeback.NewService(&fakeStore{recorded: map[string]models.StoredChargeback{}})
}

func validChargeback() models.Chargeback {
	return models.Chargeback{
		ChargebackID:  "cb-1",
		TransactionID: "txn-1",
		Reason:        models.ChargebackFraudCardNotPresent,
		AmountMinor:   5000,
		ReceivedAt:    now.Add(-time.Hour),
	}
}

func TestRecordSavesAValidChargeback(t *testing.T) {
	service := newService()
	in := validChargeback()
	in.ChargebackID, in.ReceivedAt = "  cb-1 ", time.Date(2026, 11, 12, 13, 0, 0, 123456789, time.FixedZone("UTC+4", 4*3600))

	stored, created, err := service.Record(context.Background(), in, now)

	if err != nil || !created {
		t.Fatalf("Record = %v, created %v; want a new chargeback", err, created)
	}
	if stored.ChargebackID != "cb-1" || !stored.ReceivedAt.Equal(time.Date(2026, 11, 12, 9, 0, 0, 123456000, time.UTC)) {
		t.Errorf("stored %+v, want the trimmed ID and the UTC time at microsecond precision", stored)
	}
}

func TestRecordReturnsTheEarlierChargebackForARepeat(t *testing.T) {
	service := newService()
	first, _, err := service.Record(context.Background(), validChargeback(), now)
	if err != nil {
		t.Fatal(err)
	}

	again, created, err := service.Record(context.Background(), validChargeback(), now.Add(time.Hour))

	if err != nil || created {
		t.Fatalf("repeat = %v, created %v; want the earlier chargeback", err, created)
	}
	if again != first {
		t.Errorf("repeat returned %+v, want %+v", again, first)
	}
}

func TestRecordRejectsTheSameIDWithDifferentContent(t *testing.T) {
	service := newService()
	if _, _, err := service.Record(context.Background(), validChargeback(), now); err != nil {
		t.Fatal(err)
	}
	changed := validChargeback()
	changed.Reason = models.ChargebackDuplicate

	_, _, err := service.Record(context.Background(), changed, now)

	if !errors.Is(err, models.ErrChargebackConflict) {
		t.Fatalf("err = %v, want ErrChargebackConflict", err)
	}
}

func TestRecordReturnsNotFoundForAnUnknownTransaction(t *testing.T) {
	in := validChargeback()
	in.TransactionID = "txn-unknown"

	_, _, err := newService().Record(context.Background(), in, now)

	if !errors.Is(err, models.ErrPurchaseNotFound) {
		t.Fatalf("err = %v, want ErrPurchaseNotFound", err)
	}
}

func TestRecordRejectsInvalidChargebacks(t *testing.T) {
	tests := []struct {
		name   string
		change func(*models.Chargeback)
		field  string
		want   string
	}{
		{"missing chargeback ID", func(c *models.Chargeback) { c.ChargebackID = " " }, "chargeback_id", "is required"},
		{"long chargeback ID", func(c *models.Chargeback) { c.ChargebackID = strings.Repeat("x", 201) }, "chargeback_id", "at most 200"},
		{"missing transaction ID", func(c *models.Chargeback) { c.TransactionID = "" }, "transaction_id", "is required"},
		{"NUL in chargeback ID", func(c *models.Chargeback) { c.ChargebackID = "cb-\x001" }, "chargeback_id", "control characters"},
		{"newline in transaction ID", func(c *models.Chargeback) { c.TransactionID = "txn-1\nx" }, "transaction_id", "control characters"},
		{"unknown reason", func(c *models.Chargeback) { c.Reason = "stolen" }, "reason_code", "must be one of"},
		{"zero amount", func(c *models.Chargeback) { c.AmountMinor = 0 }, "amount_minor", "positive"},
		{"amount above the purchase", func(c *models.Chargeback) { c.AmountMinor = 5001 }, "amount_minor", "exceed the purchase"},
		{"missing receive time", func(c *models.Chargeback) { c.ReceivedAt = time.Time{} }, "received_at", "is required"},
		{"receive time in the future", func(c *models.Chargeback) { c.ReceivedAt = now.Add(6 * time.Minute) }, "received_at", "future"},
		{"received before the purchase", func(c *models.Chargeback) { c.ReceivedAt = purchased.EventTime.Add(-time.Second) }, "received_at", "before the purchase"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validChargeback()
			tt.change(&in)

			_, _, err := newService().Record(context.Background(), in, now)

			var invalid *chargeback.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if !strings.Contains(invalid.Fields[tt.field], tt.want) {
				t.Errorf("fields = %v, want %s to contain %q", invalid.Fields, tt.field, tt.want)
			}
		})
	}
}
