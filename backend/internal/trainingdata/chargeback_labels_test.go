package trainingdata_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/trainingdata"
)

var (
	labelsAsOf = time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	old        = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	recent     = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
)

// charged saves a purchase with the outcome and, for a review, the analyst's
// resolution ("" leaves it undecided).
func charged(t *testing.T, tx pgx.Tx, id string, at time.Time, outcome scoring.Outcome, resolution string) {
	t.Helper()
	ctx := context.Background()
	p := models.Purchase{TransactionID: id, EventTime: at, AmountMinor: 1000, PlayerID: "player-" + id, CardToken: "card"}
	d := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: outcome}, ModelInputs: map[string]float64{}}
	if _, err := storage.NewPurchaseStore(tx).Save(ctx, p, d); err != nil {
		t.Fatalf("Save %s: %v", id, err)
	}
	if resolution == "" {
		return
	}
	_, err := tx.Exec(ctx, `INSERT INTO review_resolutions (purchase_id, decision, analyst_id)
		SELECT id, $2, 'analyst' FROM purchases WHERE transaction_id = $1`, id, resolution)
	if err != nil {
		t.Fatalf("resolve %s: %v", id, err)
	}
}

func chargedBack(t *testing.T, tx pgx.Tx, id string, reason models.ChargebackReason, receivedAt time.Time) {
	t.Helper()
	_, err := tx.Exec(context.Background(), `INSERT INTO chargebacks (chargeback_id, purchase_id, reason_code, amount_minor, received_at)
		SELECT 'cb-' || $1, id, $2, 1000, $3 FROM purchases WHERE transaction_id = $1`, id, string(reason), receivedAt)
	if err != nil {
		t.Fatalf("charge back %s: %v", id, err)
	}
}

// Recent purchases are left out even with a fraud chargeback, so recent days
// do not look almost all fraud.
func TestExportChargebackLabels(t *testing.T) {
	tx := emptyTx(t)
	allow, review, block := scoring.OutcomeAllow, scoring.OutcomeReview, scoring.OutcomeBlock
	charged(t, tx, "old-fraud", old, allow, "")
	chargedBack(t, tx, "old-fraud", models.ChargebackFraudCardNotPresent, old.AddDate(0, 0, 30))
	charged(t, tx, "old-clean", old, allow, "")
	charged(t, tx, "old-service-dispute", old, allow, "")
	chargedBack(t, tx, "old-service-dispute", models.ChargebackProductNotReceived, old.AddDate(0, 0, 10))
	charged(t, tx, "old-not-recognised", old, allow, "")
	chargedBack(t, tx, "old-not-recognised", models.ChargebackNotRecognised, old.AddDate(0, 0, 40))
	charged(t, tx, "old-fraud-after-as-of", old, allow, "")
	chargedBack(t, tx, "old-fraud-after-as-of", models.ChargebackFraudCardNotPresent, labelsAsOf.Add(time.Hour))
	charged(t, tx, "old-approved", old, review, "approve")
	charged(t, tx, "old-rejected", old, review, "reject")
	charged(t, tx, "old-undecided", old, review, "")
	charged(t, tx, "old-blocked", old, block, "")
	charged(t, tx, "recent-fraud", recent, allow, "")
	chargedBack(t, tx, "recent-fraud", models.ChargebackFraudCardAbsentOther, recent.AddDate(0, 0, 5))
	charged(t, tx, "recent-clean", recent, allow, "")
	charged(t, tx, "recent-duplicate", recent, allow, "")
	chargedBack(t, tx, "recent-duplicate", models.ChargebackDuplicate, recent.AddDate(0, 0, 3))
	charged(t, tx, "after-as-of", labelsAsOf.Add(time.Minute), allow, "")

	var buf bytes.Buffer
	sum, err := trainingdata.ExportChargebackLabels(context.Background(), tx,
		trainingdata.LabelOptions{AsOf: labelsAsOf, MaturityDays: 60}, &buf)

	if err != nil {
		t.Fatalf("ExportChargebackLabels: %v", err)
	}
	if sum != (trainingdata.LabelSummary{Fraud: 1, Legitimate: 5, Immature: 3}) {
		t.Errorf("summary = %+v, want 1 fraud, 5 legitimate and 3 immature", sum)
	}
	got, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"transaction_id", "is_fraud"},
		{"old-approved", "0"},
		{"old-clean", "0"},
		{"old-fraud", "1"},
		{"old-fraud-after-as-of", "0"},
		{"old-not-recognised", "0"},
		{"old-service-dispute", "0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("labels =\n%v\nwant\n%v", got, want)
	}
}

func TestExportChargebackLabelsMaturityWindowBoundary(t *testing.T) {
	tx := emptyTx(t)
	boundary := labelsAsOf.AddDate(0, 0, -60)
	charged(t, tx, "exactly-mature", boundary, scoring.OutcomeAllow, "")
	charged(t, tx, "one-second-too-recent", boundary.Add(time.Second), scoring.OutcomeAllow, "")

	var buf bytes.Buffer
	sum, err := trainingdata.ExportChargebackLabels(context.Background(), tx,
		trainingdata.LabelOptions{AsOf: labelsAsOf, MaturityDays: 60}, &buf)

	if err != nil {
		t.Fatalf("ExportChargebackLabels: %v", err)
	}
	if sum != (trainingdata.LabelSummary{Legitimate: 1, Immature: 1}) {
		t.Errorf("summary = %+v, want 1 legitimate and 1 immature", sum)
	}
	if got := buf.String(); got != "transaction_id,is_fraud\nexactly-mature,0\n" {
		t.Errorf("labels = %q", got)
	}
}

func exportLabels(t *testing.T, tx pgx.Tx, opts trainingdata.LabelOptions) (trainingdata.LabelSummary, string) {
	t.Helper()
	var buf bytes.Buffer
	sum, err := trainingdata.ExportChargebackLabels(context.Background(), tx, opts, &buf)
	if err != nil {
		t.Fatalf("ExportChargebackLabels: %v", err)
	}
	return sum, buf.String()
}

func TestExportChargebackLabelsWithoutMaturityWindowLabelsEveryPurchaseUpToAsOf(t *testing.T) {
	tx := emptyTx(t)
	charged(t, tx, "recent-fraud", recent, scoring.OutcomeAllow, "")
	chargedBack(t, tx, "recent-fraud", models.ChargebackFraudCardNotPresent, recent.AddDate(0, 0, 5))
	charged(t, tx, "at-as-of", labelsAsOf, scoring.OutcomeAllow, "")
	charged(t, tx, "after-as-of", labelsAsOf.Add(time.Second), scoring.OutcomeAllow, "")

	sum, got := exportLabels(t, tx, trainingdata.LabelOptions{AsOf: labelsAsOf, MaturityDays: 0})

	if sum != (trainingdata.LabelSummary{Fraud: 1, Legitimate: 1}) {
		t.Errorf("summary = %+v, want 1 fraud and 1 legitimate", sum)
	}
	if got != "transaction_id,is_fraud\nrecent-fraud,1\nat-as-of,0\n" {
		t.Errorf("labels = %q", got)
	}
}

func TestExportChargebackLabelsKeepsOnlyTheEventTimeRange(t *testing.T) {
	tx := emptyTx(t)
	since, until := old, old.AddDate(0, 0, 1)
	charged(t, tx, "before-since", since.Add(-time.Second), scoring.OutcomeAllow, "")
	charged(t, tx, "at-since", since, scoring.OutcomeAllow, "")
	charged(t, tx, "before-until", until.Add(-time.Second), scoring.OutcomeAllow, "")
	charged(t, tx, "at-until", until, scoring.OutcomeAllow, "")

	tests := []struct {
		name         string
		since, until time.Time
		want         string
	}{
		{"both", since, until, "transaction_id,is_fraud\nat-since,0\nbefore-until,0\n"},
		{"since only", since, time.Time{}, "transaction_id,is_fraud\nat-since,0\nbefore-until,0\nat-until,0\n"},
		{"until only", time.Time{}, until, "transaction_id,is_fraud\nbefore-since,0\nat-since,0\nbefore-until,0\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := exportLabels(t, tx, trainingdata.LabelOptions{AsOf: labelsAsOf, MaturityDays: 60, Since: tt.since, Until: tt.until})

			if got != tt.want {
				t.Errorf("labels = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExportChargebackLabelsRejectsInvalidOptions(t *testing.T) {
	tests := map[string]trainingdata.LabelOptions{
		"negative maturity window": {AsOf: labelsAsOf, MaturityDays: -1},
		"until before since":       {AsOf: labelsAsOf, Since: old, Until: old.Add(-time.Hour)},
		"until equal to since":     {AsOf: labelsAsOf, Since: old, Until: old},
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if _, err := trainingdata.ExportChargebackLabels(context.Background(), nil, opts, &buf); err == nil {
				t.Fatal("ExportChargebackLabels accepted invalid options")
			}
		})
	}
}
