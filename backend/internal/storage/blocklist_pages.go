package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

const (
	countBlocklistEntries = `SELECT count(*) FROM blocklist_entries`
	blocklistPageEntries  = `SELECT id, entry_type, value, reason, created_at FROM blocklist_entries`
	blocklistTextFilter   = `(strpos(lower(value), lower($%[1]d)) > 0 OR strpos(lower(reason), lower($%[1]d)) > 0)`
	blocklistPageOrder    = ` ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`
)

type blocklistFilter struct {
	where string
	args  []any
}

// ListPage keeps console pagination separate from the complete List used by Redis reloads.
func (s *BlocklistStore) ListPage(ctx context.Context, query models.BlocklistQuery) (models.BlocklistPage, error) {
	pool, ok := s.conn.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	})
	if !ok {
		return readBlocklistPage(ctx, s.conn, query)
	}
	var page models.BlocklistPage
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		page, err = readBlocklistPage(ctx, tx, query)
		return err
	})
	return page, err
}

func readBlocklistPage(ctx context.Context, conn platform.DBTX, query models.BlocklistQuery) (models.BlocklistPage, error) {
	filter := blocklistQueryFilter(query)
	total, err := countFilteredBlocklist(ctx, conn, filter)
	if err != nil {
		return models.BlocklistPage{}, err
	}
	totalPages := max(int64(1), (total-1)/int64(query.PageSize)+1)
	page := models.BlocklistPage{
		Entries: []models.StoredBlocklistEntry{},
		Pagination: models.BlocklistPagination{
			Page: min(query.Page, totalPages), PageSize: query.PageSize, TotalPages: totalPages, TotalItems: total,
		},
	}
	if total == 0 {
		return page, nil
	}
	page.Entries, err = readBlocklistPageEntries(ctx, conn, filter, page.Pagination)
	return page, err
}

func blocklistQueryFilter(query models.BlocklistQuery) blocklistFilter {
	var filter blocklistFilter
	var clauses []string
	if query.Type != "" {
		filter.args = append(filter.args, string(query.Type))
		clauses = append(clauses, `entry_type = $1`)
	}
	if query.Query != "" {
		filter.args = append(filter.args, query.Query)
		clauses = append(clauses, fmt.Sprintf(blocklistTextFilter, len(filter.args)))
	}
	if len(clauses) > 0 {
		filter.where = ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	return filter
}

func countFilteredBlocklist(ctx context.Context, conn platform.DBTX, filter blocklistFilter) (int64, error) {
	var total int64
	if err := conn.QueryRow(ctx, countBlocklistEntries+filter.where, filter.args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count blocklist entries: %w", err)
	}
	return total, nil
}

func readBlocklistPageEntries(ctx context.Context, conn platform.DBTX, filter blocklistFilter, pagination models.BlocklistPagination) ([]models.StoredBlocklistEntry, error) {
	sql := blocklistPageEntries + filter.where + fmt.Sprintf(blocklistPageOrder, len(filter.args)+1, len(filter.args)+2)
	offset := (pagination.Page - 1) * int64(pagination.PageSize)
	args := append(filter.args, pagination.PageSize, offset)
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("read blocklist page: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.StoredBlocklistEntry, error) {
		var entry models.StoredBlocklistEntry
		err := row.Scan(&entry.ID, &entry.Type, &entry.Value, &entry.Reason, &entry.CreatedAt)
		entry.CreatedAt = entry.CreatedAt.UTC()
		return entry, err
	})
	if err != nil {
		return nil, fmt.Errorf("read blocklist page: %w", err)
	}
	return entries, nil
}
