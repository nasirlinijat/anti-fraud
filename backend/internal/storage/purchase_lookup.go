package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const searchPurchasesSQL = `
SELECT p.id, p.transaction_id, p.player_id, p.amount_minor, p.event_time, d.outcome,
       CASE
         WHEN d.outcome <> 'review' THEN ''
         WHEN r.purchase_id IS NULL THEN 'open'
         WHEN r.decision = 'approve' THEN 'approved'
         ELSE 'rejected'
       END,
       CASE
         WHEN p.transaction_id = $1 THEN 'transaction'
         WHEN p.player_id = $1 THEN 'player'
         ELSE 'card'
       END
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
LEFT JOIN review_resolutions r ON r.purchase_id = p.id
WHERE p.transaction_id = $1 OR p.player_id = $1 OR p.card_token = $1
ORDER BY p.event_time DESC, p.id DESC
LIMIT $2`

const getPurchaseCase = `
SELECT ` + caseEvidenceColumns + `, d.outcome, r.decision, r.analyst_id, r.resolved_at
FROM ` + queueItemTables + `
LEFT JOIN review_resolutions r ON r.purchase_id = d.purchase_id
WHERE d.purchase_id = $1`

func (s *ReviewStore) SearchPurchases(ctx context.Context, query string, limit int) ([]models.PurchaseMatch, error) {
	rows, err := s.conn.Query(ctx, searchPurchasesSQL, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search purchases: %w", err)
	}
	matches, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.PurchaseMatch, error) {
		var m models.PurchaseMatch
		err := row.Scan(&m.PurchaseID, &m.TransactionID, &m.PlayerID, &m.AmountMinor, &m.EventTime, &m.Outcome, &m.Review, &m.MatchedBy)
		m.EventTime = m.EventTime.UTC()
		return m, err
	})
	if err != nil {
		return nil, fmt.Errorf("read purchase matches: %w", err)
	}
	return matches, nil
}

func (s *ReviewStore) PurchaseCase(ctx context.Context, purchaseID int64) (models.PurchaseCase, error) {
	var outcome string
	var decision, analyst *string
	var resolvedAt *time.Time
	c, err := s.readCaseWith(ctx, s.conn.QueryRow(ctx, getPurchaseCase, purchaseID), &outcome, &decision, &analyst, &resolvedAt)
	if errors.Is(err, models.ErrReviewNotFound) {
		return models.PurchaseCase{}, models.ErrPurchaseNotFound
	}
	if err != nil {
		return models.PurchaseCase{}, err
	}
	if decision != nil && analyst != nil && resolvedAt != nil {
		c.Resolution = &models.CaseResolution{Decision: *decision, AnalystID: *analyst, ResolvedAt: resolvedAt.UTC()}
	}
	return models.PurchaseCase{CaseItem: c, Outcome: outcome}, nil
}
