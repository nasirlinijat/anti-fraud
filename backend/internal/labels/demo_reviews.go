package labels

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// DemoReview identifies an open review and its private simulator label.
type DemoReview struct {
	PurchaseID int64
	Fraud      bool
}

// Keep the newest reviews open. The transaction ID breaks event-time ties
// independently of database identity sequences, which demo reset preserves.
const demoReviewsSQL = `
SELECT p.id, l.is_fraud
FROM purchase_labels l
JOIN purchases p ON p.transaction_id = l.transaction_id
JOIN decisions d ON d.purchase_id = p.id
WHERE l.source = 'simulator' AND l.run_id = $1 AND d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = p.id)
ORDER BY p.event_time DESC, p.transaction_id DESC`

// DemoReviews returns the open reviews of a simulator run, newest first.
func DemoReviews(ctx context.Context, conn platform.DBTX, run string) ([]DemoReview, error) {
	rows, err := conn.Query(ctx, demoReviewsSQL, run)
	if err != nil {
		return nil, fmt.Errorf("list demo reviews: %w", err)
	}
	reviews, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DemoReview, error) {
		var review DemoReview
		err := row.Scan(&review.PurchaseID, &review.Fraud)
		return review, err
	})
	if err != nil {
		return nil, fmt.Errorf("list demo reviews: %w", err)
	}
	return reviews, nil
}
