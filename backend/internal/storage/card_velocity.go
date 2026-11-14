package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const cardVelocityContextLimit = 20

// Fetch every saved reference separately from the limited investigation context.
const caseCardVelocitySQL = `
WITH card AS (SELECT card_token FROM purchases WHERE id = $1)
SELECT transaction_id, event_time, amount_minor, received_at, true AS referenced
FROM purchases
WHERE id = ANY($2::bigint[])
  AND card_token = (SELECT card_token FROM card)
  AND event_time > $3 AND event_time <= $4 AND id <> $1
UNION ALL
(
    SELECT transaction_id, event_time, amount_minor, received_at, false AS referenced
    FROM purchases
    WHERE card_token = (SELECT card_token FROM card)
      AND event_time > $3 AND event_time <= $4 AND id <> $1
      AND NOT (id = ANY($2::bigint[]))
    ORDER BY event_time DESC, transaction_id DESC
    LIMIT $5
)
ORDER BY event_time, transaction_id`

func (s *ReviewStore) caseCardVelocity(ctx context.Context, item models.QueueItem, saved []byte) (*models.CardVelocityEvidence, error) {
	if saved == nil {
		return nil, nil
	}
	var evidence models.CardVelocityEvidence
	if err := json.Unmarshal(saved, &evidence.Snapshot); err != nil {
		return nil, fmt.Errorf("decode card velocity observation: %w", err)
	}
	snapshot := evidence.Snapshot
	ids := []int64{}
	if snapshot.Count <= models.CardVelocityReferenceLimit && len(snapshot.PurchaseIDs) == snapshot.Count {
		ids = append(ids, snapshot.PurchaseIDs...)
	}
	purchases, err := s.cardVelocityPurchases(ctx, item, snapshot, ids)
	if err != nil {
		return nil, err
	}
	return finishCardVelocityEvidence(evidence, purchases), nil
}

func (s *ReviewStore) cardVelocityPurchases(ctx context.Context, item models.QueueItem, snapshot models.CardVelocitySnapshot, ids []int64) ([]models.CardVelocityPurchase, error) {
	rows, err := s.conn.Query(ctx, caseCardVelocitySQL, item.PurchaseID, ids, snapshot.WindowStart, snapshot.WindowEnd, cardVelocityContextLimit+1)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.CardVelocityPurchase, error) {
		var p models.CardVelocityPurchase
		var receivedAt time.Time
		err := row.Scan(&p.TransactionID, &p.EventTime, &p.AmountMinor, &receivedAt, &p.Contributed)
		p.EventTime = p.EventTime.UTC()
		p.RecordedAfterDecision = !p.Contributed && receivedAt.After(item.DecidedAt)
		return p, err
	})
}

func finishCardVelocityEvidence(evidence models.CardVelocityEvidence, purchases []models.CardVelocityPurchase) *models.CardVelocityEvidence {
	verified := 0
	for _, p := range purchases {
		if p.Contributed {
			verified++
		}
	}
	evidence.RecordsAvailable = evidence.Snapshot.Count > 0 && verified == evidence.Snapshot.Count
	evidence.Purchases = make([]models.CardVelocityPurchase, 0, len(purchases))
	// The query reads one extra context row to detect truncation, then sorts oldest first.
	skipOldest := len(purchases)-verified > cardVelocityContextLimit
	evidence.ContextTruncated = skipOldest
	for _, p := range purchases {
		if !p.Contributed && skipOldest {
			skipOldest = false
			continue
		}
		p.Contributed = p.Contributed && evidence.RecordsAvailable
		evidence.Purchases = append(evidence.Purchases, p)
	}
	return &evidence
}
