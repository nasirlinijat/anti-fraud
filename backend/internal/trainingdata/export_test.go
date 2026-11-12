package trainingdata_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/trainingdata"
)

func emptyTx(t *testing.T) pgx.Tx {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(ctx) })
	for _, table := range []string{"review_resolutions", "review_claims", "decisions", "purchases", "feature_counts"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("empty %s: %v", table, err)
		}
	}
	return tx
}

func ptr[T any](v T) *T { return &v }

func save(t *testing.T, tx pgx.Tx, id string, at time.Time, inputs map[string]float64, modelScore *float64) {
	t.Helper()
	p := models.Purchase{TransactionID: id, EventTime: at, AmountMinor: 1000, PlayerID: "player-" + id, CardToken: "card"}
	d := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}, ModelInputs: inputs, ModelScore: modelScore}
	if _, err := storage.NewPurchaseStore(tx).Save(context.Background(), p, d); err != nil {
		t.Fatalf("Save %s: %v", id, err)
	}
}

func TestExport(t *testing.T) {
	tx := emptyTx(t)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	names := []string{"amount", "cents"}
	save(t, tx, "b", at, map[string]float64{"amount": 20.5, "cents": 0.5}, ptr(0.25))
	save(t, tx, "a", at, map[string]float64{"amount": 10, "cents": 0}, nil)
	save(t, tx, "c", at.Add(time.Minute), map[string]float64{"amount": 1, "hour_of_day": 3}, nil)
	save(t, tx, "d", at.Add(2*time.Minute), map[string]float64{}, nil)

	var buf bytes.Buffer
	sum, err := trainingdata.Export(context.Background(), tx, names, &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if sum != (trainingdata.Summary{Rows: 2, Skipped: 2}) {
		t.Errorf("summary = %+v, want 2 rows and 2 skipped", sum)
	}
	got, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"transaction_id", "event_time", "player_id", "model_score", "amount", "cents"},
		{"a", "2026-11-10T09:00:00Z", "player-a", "", "10", "0"},
		{"b", "2026-11-10T09:00:00Z", "player-b", "0.25", "20.5", "0.5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("export =\n%v\nwant\n%v", got, want)
	}
}

func TestExportReadsEveryPage(t *testing.T) {
	tx := emptyTx(t)
	defer func(size int32) { trainingdata.PageSize = size }(trainingdata.PageSize)
	trainingdata.PageSize = 2
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	// Two purchases share each second, so pages also split equal event times.
	total := 7
	for i := range total {
		save(t, tx, string(rune('a'+i)), at.Add(time.Duration(i/2)*time.Second), map[string]float64{"amount": float64(i)}, nil)
	}
	var buf bytes.Buffer
	sum, err := trainingdata.Export(context.Background(), tx, []string{"amount"}, &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if sum.Rows != total {
		t.Fatalf("exported %d rows, want %d", sum.Rows, total)
	}
	rows, _ := csv.NewReader(&buf).ReadAll()
	for i, row := range rows[1:] {
		if row[0] != string(rune('a'+i)) {
			t.Fatalf("row %d is %s, want %c", i, row[0], 'a'+i)
		}
	}
}
