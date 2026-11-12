package labels_test

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func setup(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	owner := os.Getenv("TEST_DATABASE_URL")
	writer := os.Getenv("TEST_LABELS_DATABASE_URL")
	if owner == "" || writer == "" {
		t.Skip("test owner and labels URLs required")
	}
	a, e := pgxpool.New(t.Context(), owner)
	if e != nil {
		t.Fatal(e)
	}
	b, e := pgxpool.New(t.Context(), writer)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close(); a.Close() })
	if e = labels.Preflight(t.Context(), b); e != nil {
		t.Fatal(e)
	}
	return a, b
}
func purchase(t *testing.T, a *pgxpool.Pool, id string) {
	t.Helper()
	_, e := a.Exec(t.Context(), `WITH p AS (INSERT INTO purchases(transaction_id,event_time,amount_minor,player_id,card_token) VALUES($1,'2026-11-10T10:00:00Z',100,$1,$1) RETURNING id) INSERT INTO decisions(purchase_id,ruleset_version,outcome,rule_score,fired_rules,model_inputs) SELECT id,(SELECT version FROM rulesets WHERE is_active),'allow',0,'[]','{}' FROM p`, id)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, e := a.Exec(context.Background(), `DELETE FROM purchase_labels WHERE transaction_id=$1`, id)
		if e != nil {
			t.Error(e)
		}
		_, _ = a.Exec(context.Background(), `DELETE FROM pending_purchase_labels WHERE transaction_id=$1`, id)
		_, _ = a.Exec(context.Background(), `DELETE FROM decisions WHERE purchase_id IN (SELECT id FROM purchases WHERE transaction_id=$1)`, id)
		_, _ = a.Exec(context.Background(), `DELETE FROM purchases WHERE transaction_id=$1`, id)
	})
}
func TestLabelIntegrityAndRecovery(t *testing.T) {
	a, b := setup(t)
	id := "af74-integrity"
	scenario := "normal"
	l := labels.Label{TransactionID: id, RunID: "af74-run", Source: "simulator", Scenario: &scenario}
	l.RequestJSON = testSnapshot(id)
	if e := labels.Prepare(t.Context(), b, l); e != nil {
		t.Fatal(e)
	}
	if e := labels.Finalize(t.Context(), b, l, nil); e == nil {
		t.Fatal("label accepted before decision")
	}
	purchase(t, a, id)
	for range 2 {
		n, e := labels.Recover(t.Context(), b, "af74-run")
		if e != nil {
			t.Fatal(e)
		}
		_ = n
	}
	if e := labels.Finalize(t.Context(), b, l, nil); e != nil {
		t.Fatal(e)
	}
	l.Fraud = true
	if e := labels.Finalize(t.Context(), b, l, nil); e == nil {
		t.Fatal("changed label accepted")
	}
	var fraud bool
	var n int
	if e := b.QueryRow(t.Context(), `SELECT is_fraud FROM purchase_labels WHERE transaction_id=$1`, id).Scan(&fraud); e != nil || fraud {
		t.Fatal(e, fraud)
	}
	if e := b.QueryRow(t.Context(), `SELECT count(*) FROM pending_purchase_labels WHERE transaction_id=$1`, id).Scan(&n); e != nil || n != 0 {
		t.Fatal(e, n)
	}
}
func TestConcurrentIdenticalLabels(t *testing.T) {
	a, b := setup(t)
	id := "af74-concurrent"
	purchase(t, a, id)
	l := labels.Label{TransactionID: id, RunID: "af74-concurrent", Source: "ieee-cis"}
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { errs <- labels.Finalize(t.Context(), b, l, nil) })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
}
func TestImportAtomicity(t *testing.T) {
	a, b := setup(t)
	for _, id := range []string{"af74-csv1", "af74-csv2"} {
		purchase(t, a, id)
	}
	for _, csv := range []string{"transaction_id,is_fraud\naf74-csv1,1\naf74-csv2,wrong\n", "transaction_id,is_fraud\naf74-csv1,1\nmissing,0\n", "transaction_id,is_fraud\naf74-csv1,1\naf74-csv1,1\n"} {
		if _, e := labels.ImportCSV(t.Context(), b, strings.NewReader(csv), "csv-run", "ieee-cis"); e == nil {
			t.Fatal("bad import accepted")
		}
	}
	var n int
	b.QueryRow(t.Context(), `SELECT count(*) FROM purchase_labels WHERE run_id='csv-run'`).Scan(&n)
	if n != 0 {
		t.Fatal("partial import", n)
	}
	csv := "transaction_id,is_fraud\naf74-csv1,1\naf74-csv2,0\n"
	for _, want := range []int64{2, 0} {
		n, e := labels.ImportCSV(t.Context(), b, strings.NewReader(csv), "csv-run", "ieee-cis")
		if e != nil || n != want {
			t.Fatal(n, e)
		}
	}
	if _, e := labels.ImportCSV(t.Context(), b, strings.NewReader("transaction_id,is_fraud\naf74-csv1,0\n"), "csv-run", "ieee-cis"); e == nil {
		t.Fatal("conflicting import accepted")
	}
}
func TestCheckpointRollsBackWithLabel(t *testing.T) {
	a, b := setup(t)
	id := "af74-rollback"
	purchase(t, a, id)
	l := labels.Label{TransactionID: id, RunID: id, Source: "ieee-cis"}
	l.RequestJSON = testSnapshot(id)
	if err := labels.Prepare(t.Context(), b, l); err != nil {
		t.Fatal(err)
	}
	e := labels.Finalize(t.Context(), b, l, func(pgx.Tx) error { return fmt.Errorf("checkpoint unavailable") })
	if e == nil {
		t.Fatal("expected rollback")
	}
	var n int
	b.QueryRow(t.Context(), `SELECT count(*) FROM purchase_labels WHERE transaction_id=$1`, id).Scan(&n)
	if n != 0 {
		t.Fatal("label escaped rollback")
	}
	b.QueryRow(t.Context(), `SELECT count(*) FROM pending_purchase_labels WHERE transaction_id=$1`, id).Scan(&n)
	if n != 1 {
		t.Fatal("intent lost")
	}
}
func TestScoringBoundary(t *testing.T) {
	for _, pkg := range []string{"score", "scoring", "features", "lgbm"} {
		files, e := os.ReadDir("../" + pkg)
		if e != nil {
			t.Fatal(e)
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".go") && !strings.HasSuffix(f.Name(), "_test.go") {
				s, _ := os.ReadFile("../" + pkg + "/" + f.Name())
				if strings.Contains(string(s), "internal/labels") || strings.Contains(string(s), "purchase_labels") {
					t.Fatal("label access in scoring package", pkg, f.Name())
				}
			}
		}
	}
	s, e := os.ReadFile("../../../openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(s), "is_fraud") || strings.Contains(string(s), "purchase_labels") {
		t.Fatal("private answers in API specification")
	}
}

func testSnapshot(id string) []byte {
	return labels.FromPurchase("run", simulator.Purchase{Pattern: simulator.PatternNormal, Request: simulator.Request{TransactionID: id, PlayerID: id, CardToken: id, AmountMinor: 100, EventTime: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}}).RequestJSON
}
