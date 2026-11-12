package labels

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

// SettledPurchase is a simulator purchase that was charged to the card, with
// its private scenario. Only the mock bank reads it, through the evaluation
// role, to decide which purchases a cardholder disputes.
type SettledPurchase struct {
	TransactionID string
	EventTime     time.Time
	AmountMinor   int64
	Pattern       simulator.Pattern
}

// settledPurchasesSQL lists the labelled simulator purchases of run $1 that
// were allowed, or reviewed and approved. Blocked, rejected and undecided
// purchases were never charged, so a bank cannot charge them back.
const settledPurchasesSQL = `
SELECT p.transaction_id, p.event_time, p.amount_minor, l.scenario
FROM purchase_labels l
JOIN purchases p ON p.transaction_id = l.transaction_id
JOIN decisions d ON d.purchase_id = p.id
LEFT JOIN review_resolutions r ON r.purchase_id = p.id
WHERE l.source = 'simulator'
  AND l.run_id = $1
  AND (d.outcome = 'allow' OR (d.outcome = 'review' AND r.decision = 'approve'))
ORDER BY p.event_time, p.transaction_id`

// SettledPurchases returns the charged purchases of a simulator run in event
// time order.
func SettledPurchases(ctx context.Context, conn platform.DBTX, run string) ([]SettledPurchase, error) {
	rows, err := conn.Query(ctx, settledPurchasesSQL, run)
	if err != nil {
		return nil, fmt.Errorf("list settled purchases: %w", err)
	}
	purchases, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (SettledPurchase, error) {
		var p SettledPurchase
		err := row.Scan(&p.TransactionID, &p.EventTime, &p.AmountMinor, &p.Pattern)
		p.EventTime = p.EventTime.UTC()
		return p, err
	})
	if err != nil {
		return nil, fmt.Errorf("list settled purchases: %w", err)
	}
	return purchases, nil
}
