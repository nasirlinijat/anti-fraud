package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestFeatureQueriesUseIndexes checks that every feature history and dashboard
// statistics query can be served by an index. Sequential scans are disabled, so a query without a
// usable index still shows a sequential scan in its plan.
func TestFeatureQueriesUseIndexes(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	if _, err := conn.Exec(ctx, "SET enable_seqscan = off"); err != nil {
		t.Fatalf("disable sequential scans: %v", err)
	}

	at := pgtype.Timestamptz{Time: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Valid: true}
	device := pgtype.Text{String: "Windows", Valid: true}
	client := pgtype.Text{String: "Windows 10", Valid: true}
	queries := []struct {
		name  string
		query string
		args  []any
	}{
		{"PlayerHistory", playerHistory, []any{at, int64(1000), "gmail.com", device, client, client, client, "player-1", "txn-1"}},
		{"CardHistory", cardHistory, []any{at, "card-1", "txn-1"}},
		{"IssuerHistory", issuerHistory, []any{"1001", at, "txn-1"}},
		{"FeatureCounts", featureCounts, []any{"gmail.com", device, client, client, client}},
		{"CountOutcomes", countOutcomes, []any{at}},
		{"TopFiredRules", topFiredRules, []any{at, int32(10)}},
		{"CountReviewQueue", countReviewQueue, nil},
		{"CountModelEffects", countModelEffects, []any{at}},
		{"ListModelInputs", listModelInputs, []any{at, "txn-1", int32(5000)}},
		{"PlayerEarlierModelScores", playerEarlierModelScores, []any{"player-1", at, "txn-1"}},
	}
	for _, q := range queries {
		t.Run(q.name, func(t *testing.T) {
			rows, err := conn.Query(ctx, "EXPLAIN "+q.query, q.args...)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatalf("read plan: %v", err)
			}
			plan := strings.Join(lines, "\n")
			if strings.Contains(plan, "Seq Scan") || !strings.Contains(plan, "Index") {
				t.Errorf("query does not use an index:\n%s", plan)
			}
		})
	}
}
