package storage

import (
	"context"
	"os"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func TestSaveStoresTheIPLookup(t *testing.T) {
	tests := []struct {
		name    string
		ipIntel *ipintel.Result
		want    *string
	}{
		{"no lookup", nil, nil},
		{"found", &ipintel.Result{Provider: "mock", Available: true, VPN: true},
			ptr(`{"provider": "mock", "available": true, "hosting": false, "vpn": true, "proxy": false, "tor": false}`)},
		{"unavailable", ptr(ipintel.Unavailable("mock")),
			ptr(`{"provider": "mock", "available": false, "hosting": false, "vpn": false, "proxy": false, "tor": false}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := testTx(t)
			d := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeReview}, IPIntel: tt.ipIntel}

			id, err := NewPurchaseStore(tx).Save(context.Background(), minimalPurchase("txn-ip-intel"), d)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}

			var matches bool
			if err := tx.QueryRow(context.Background(),
				`SELECT ip_intel IS NOT DISTINCT FROM $2::jsonb FROM decisions WHERE purchase_id = $1`, id, tt.want).Scan(&matches); err != nil {
				t.Fatal(err)
			}
			if !matches {
				t.Errorf("ip_intel differs from %s", deref(tt.want))
			}
			reviewCase, err := NewReviewStore(tx).Case(context.Background(), id)
			if err != nil || (reviewCase.IPIntel == nil) != (tt.ipIntel == nil) || (tt.ipIntel != nil && *reviewCase.IPIntel != *tt.ipIntel) {
				t.Errorf("Case IP intelligence = %+v, %v; want %+v", reviewCase.IPIntel, err, tt.ipIntel)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return "NULL"
	}
	return *s
}

// The migration runs inside the test transaction, which rolls back, so the
// shared test database keeps its schema.
func TestIPIntelMigrationDownAndUp(t *testing.T) {
	tx := testTx(t)
	const columns = `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'decisions' AND column_name = 'ip_intel'`
	for _, step := range []struct {
		file        string
		wantColumns int
	}{
		{"../../migrations/000030_save_ip_intel.down.sql", 0},
		{"../../migrations/000030_save_ip_intel.up.sql", 1},
	} {
		migration, err := os.ReadFile(step.file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(context.Background(), string(migration)); err != nil {
			t.Fatalf("apply %s: %v", step.file, err)
		}
		if got := countRows(t, tx, columns); got != step.wantColumns {
			t.Errorf("after %s: %d ip_intel columns, want %d", step.file, got, step.wantColumns)
		}
	}
}
