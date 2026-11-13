package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

const countClaimedReviewQueue = `
SELECT count(*)
FROM decisions d
JOIN review_claims rc ON rc.purchase_id = d.purchase_id
JOIN accounts ca ON ca.id = rc.account_id
` + openReviewFilter + ` AND ca.username = $1`

// QueuePage reads a numbered page and count from the same database snapshot.
func (s *ReviewStore) QueuePage(ctx context.Context, limit int, order models.QueueOrder, number int64, claimedBy *string) (models.NumberedReviewPage, error) {
	// A store already bound to a transaction uses the caller's snapshot.
	pool, ok := s.conn.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	})
	if !ok {
		return readNumberedQueue(ctx, s.conn, numberedQueueQuery{limit, order, number, claimedBy})
	}
	var page models.NumberedReviewPage
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		page, err = readNumberedQueue(ctx, tx, numberedQueueQuery{limit, order, number, claimedBy})
		return err
	})
	return page, err
}

type numberedQueueQuery struct {
	limit     int
	order     models.QueueOrder
	number    int64
	claimedBy *string
}

func readNumberedQueue(ctx context.Context, conn platform.DBTX, query numberedQueueQuery) (models.NumberedReviewPage, error) {
	countSQL := CountReviewQueueSQL
	var args []any
	if query.claimedBy != nil {
		countSQL = countClaimedReviewQueue
		args = []any{*query.claimedBy}
	}
	var page models.NumberedReviewPage
	if err := conn.QueryRow(ctx, countSQL, args...).Scan(&page.TotalItems); err != nil {
		return page, fmt.Errorf("count review queue: %w", err)
	}
	page.Number = min(query.number, models.ReviewPageCount(page.TotalItems, query.limit))
	offset := (page.Number - 1) * int64(query.limit)
	items, err := readQueueRows(ctx, conn, queueSQL(query.order), nil, nil, nil, int32(query.limit), query.claimedBy, offset)
	page.Items = items
	return page, err
}
