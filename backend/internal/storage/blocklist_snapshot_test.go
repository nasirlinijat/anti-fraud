package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

type blocklistSnapshotPool struct {
	*pgxpool.Pool
	afterCount func()
}

func (p blocklistSnapshotPool) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := p.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return blocklistSnapshotTx{Tx: tx, afterCount: p.afterCount}, nil
}

type blocklistSnapshotTx struct {
	pgx.Tx
	afterCount func()
}

func (tx blocklistSnapshotTx) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	row := tx.Tx.QueryRow(ctx, query, args...)
	if strings.HasPrefix(query, countBlocklistEntries) {
		return blocklistCountRow{Row: row, afterCount: tx.afterCount}
	}
	return row
}

type blocklistCountRow struct {
	pgx.Row
	afterCount func()
}

func (row blocklistCountRow) Scan(dest ...any) error {
	if err := row.Row.Scan(dest...); err != nil {
		return err
	}
	row.afterCount()
	return nil
}

func TestBlocklistPageCountAndRowsShareASnapshot(t *testing.T) {
	pool := testPool(t)
	const prefix = "af197-snapshot-"
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM blocklist_entries WHERE value IN ($1,$2)`, prefix+"first", prefix+"later"); err != nil {
			t.Error(err)
		}
	})
	if _, err := pool.Exec(t.Context(), `INSERT INTO blocklist_entries(entry_type,value,reason) VALUES('card',$1,'r')`, prefix+"first"); err != nil {
		t.Fatal(err)
	}
	interleaved := blocklistSnapshotPool{Pool: pool, afterCount: func() {
		if _, err := pool.Exec(t.Context(), `INSERT INTO blocklist_entries(entry_type,value,reason) VALUES('card',$1,'r')`, prefix+"later"); err != nil {
			t.Fatal(err)
		}
	}}
	query := models.BlocklistQuery{Query: prefix, Page: 1, PageSize: 25}
	page, err := NewBlocklistStore(interleaved).ListPage(t.Context(), query)
	if err != nil || page.Pagination.TotalItems != 1 || len(page.Entries) != 1 || page.Entries[0].Value != prefix+"first" {
		t.Fatalf("snapshot page %+v, err %v", page, err)
	}
	page, err = NewBlocklistStore(pool).ListPage(t.Context(), query)
	if err != nil || page.Pagination.TotalItems != 2 || len(page.Entries) != 2 {
		t.Fatalf("next request %+v, err %v", page, err)
	}
}
