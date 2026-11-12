package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func TestFindSavedDecisionComparesVerificationResults(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	store := NewPurchaseStore(tx)
	saved := fullPurchase()
	d := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}
	if _, err := store.Save(ctx, saved, d); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tests := []struct {
		name    string
		change  func(p *models.Purchase)
		wantErr error
	}{
		{"same results", func(*models.Purchase) {}, nil},
		{"other CVV result", func(p *models.Purchase) { p.CVVResult = ptr(scoring.CVVMismatch) }, models.ErrDuplicateTransaction},
		{"AVS result left out", func(p *models.Purchase) { p.AVSResult = nil }, models.ErrDuplicateTransaction},
		{"other 3-D Secure result", func(p *models.Purchase) { p.ThreeDSResult = ptr(scoring.ThreeDSAuthenticated) }, models.ErrDuplicateTransaction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retry := fullPurchase()
			tt.change(&retry)

			got, err := store.FindSavedDecision(ctx, retry)

			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && got == nil) {
				t.Errorf("FindSavedDecision = %+v, %v; want error %v", got, err, tt.wantErr)
			}
		})
	}
}

func TestCaseReturnsPaymentVerification(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	withResults := insertReviewDecision(t, tx, "c-verified", "review", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil, `[]`)
	withoutResults := insertReviewDecision(t, tx, "c-unverified", "review", time.Date(2000, 1, 1, 9, 1, 0, 0, time.UTC), nil, `[]`)
	if _, err := tx.Exec(ctx, `UPDATE purchases SET cvv_result = 'mismatch', avs_result = 'partial', three_ds_result = 'failed' WHERE id = $1`, withResults); err != nil {
		t.Fatalf("set verification results: %v", err)
	}
	store := NewReviewStore(tx)

	got, err := store.Case(ctx, withResults)
	if err != nil || got.CVVResult == nil || *got.CVVResult != scoring.CVVMismatch ||
		got.AVSResult == nil || *got.AVSResult != scoring.AVSPartial ||
		got.ThreeDSResult == nil || *got.ThreeDSResult != scoring.ThreeDSFailed {
		t.Errorf("Case(with results) = %v, %v, %v, %v; want mismatch, partial, failed", got.CVVResult, got.AVSResult, got.ThreeDSResult, err)
	}
	got, err = store.Case(ctx, withoutResults)
	if err != nil || got.CVVResult != nil || got.AVSResult != nil || got.ThreeDSResult != nil {
		t.Errorf("Case(without results) = %v, %v, %v, %v; want none", got.CVVResult, got.AVSResult, got.ThreeDSResult, err)
	}
}

func TestPurchasesRejectUnknownVerificationResults(t *testing.T) {
	tx := testTx(t)
	id := insertReviewDecision(t, tx, "c-bad-result", "review", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil, `[]`)

	_, err := tx.Exec(context.Background(), `UPDATE purchases SET cvv_result = 'passed' WHERE id = $1`, id)

	if err == nil {
		t.Error("an unknown CVV result was saved")
	}
}

// The migration runs inside the test transaction, which rolls back, so the
// shared test database keeps its schema.
func TestPaymentVerificationMigrationDownAndUp(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	const columns = `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'purchases'
		  AND column_name IN ('cvv_result', 'avs_result', 'three_ds_result')`
	for _, step := range []struct {
		file        string
		wantColumns int
	}{
		{"../../migrations/000027_add_payment_verification.down.sql", 0},
		{"../../migrations/000027_add_payment_verification.up.sql", 3},
	} {
		migration, err := os.ReadFile(step.file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", step.file, err)
		}
		if got := countRows(t, tx, columns); got != step.wantColumns {
			t.Errorf("after %s: %d verification columns, want %d", step.file, got, step.wantColumns)
		}
	}
}
