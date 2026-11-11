package transport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

func testPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

func testTx(t *testing.T) pgx.Tx {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(ctx) })
	return tx
}

func minimalPurchase(transactionID string) models.Purchase {
	return models.Purchase{
		TransactionID: transactionID,
		EventTime:     time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
		AmountMinor:   1999,
		PlayerID:      "player-1",
		CardToken:     "card-1",
	}
}

func testIsolatedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := t.Context()
	pool := testPool(t)
	schema := fmt.Sprintf("score_retries_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config := pool.Config()
	config.MaxConns = max(config.MaxConns, 3)
	config.ConnConfig.RuntimeParams["search_path"] = schema
	isolated, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(isolated.Close)

	migrations, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find migrations: %v, found %d", err, len(migrations))
	}
	for _, path := range migrations {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := isolated.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}
	return isolated
}

func ptr[T any](v T) *T { return &v }

func countRows(t *testing.T, conn storage.Conn, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
