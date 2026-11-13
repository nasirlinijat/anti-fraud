package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const linkedReviewLimit = 50

// LinkedReviewsSQL lists the purchases other than $6 that await review and
// share device $1, card $2, or player $3 between $4 and $5, in the default
// queue order, with the number of all matching purchases. $7 is the page
// size. A NULL device links nothing.
//
// Each anchor is its own branch so that each can use its own event-time
// index, and UNION removes a purchase that shares several anchors before the
// count, the order, and the limit apply to the merged set.
const LinkedReviewsSQL = `
WITH linked AS (
    SELECT id FROM purchases
    WHERE device_id IS NOT NULL
      AND device_id = $1::text
      AND event_time BETWEEN $4::timestamptz AND $5::timestamptz
    UNION
    SELECT id FROM purchases
    WHERE card_token = $2::text
      AND event_time BETWEEN $4::timestamptz AND $5::timestamptz
    UNION
    SELECT id FROM purchases
    WHERE player_id = $3::text
      AND event_time BETWEEN $4::timestamptz AND $5::timestamptz
)
SELECT ` + queueItemColumns + `, count(*) OVER () AS total
FROM ` + queueItemTables + `
JOIN linked l ON l.id = d.purchase_id
WHERE d.purchase_id <> $6::bigint
  AND d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)
ORDER BY -d.rule_score, d.decided_at, d.purchase_id
LIMIT $7`

// LinkedReviews returns the other purchases awaiting review that share the
// device, card, or player of the purchase under review within the network
// window, highest rule score first. It returns models.ErrReviewNotFound if
// the purchase is not under review.
func (s *ReviewStore) LinkedReviews(ctx context.Context, purchaseID int64) (models.LinkedReviews, error) {
	seed, err := s.networkSeed(ctx, purchaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.LinkedReviews{}, models.ErrReviewNotFound
	}
	if err != nil {
		return models.LinkedReviews{}, fmt.Errorf("get network seed for case %d: %w", purchaseID, err)
	}
	from := seed.eventTime.AddDate(0, 0, -networkWindowDays)
	to := seed.eventTime.AddDate(0, 0, networkWindowDays)
	rows, err := s.conn.Query(ctx, LinkedReviewsSQL,
		seed.deviceID, seed.cardToken, seed.playerID, from, to, purchaseID, int32(linkedReviewLimit))
	if err != nil {
		return models.LinkedReviews{}, fmt.Errorf("list linked reviews for case %d: %w", purchaseID, err)
	}
	result := models.LinkedReviews{WindowDays: networkWindowDays}
	result.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.QueueItem, error) {
		return scanQueueItem(row, &result.Total)
	})
	if err != nil {
		return models.LinkedReviews{}, fmt.Errorf("list linked reviews for case %d: %w", purchaseID, err)
	}
	return result, nil
}
