package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// ChargebackStore records chargebacks from the processor webhook. It is the
// only store that touches the chargebacks table; scoring, model inputs, rules
// and reviews never read it.
type ChargebackStore struct {
	conn platform.DBTX
}

func NewChargebackStore(conn platform.DBTX) *ChargebackStore {
	return &ChargebackStore{conn: conn}
}

const chargedPurchaseSQL = `
SELECT id, event_time, amount_minor
FROM purchases
WHERE transaction_id = $1`

// Purchase returns the saved purchase with the transaction ID, or
// models.ErrPurchaseNotFound.
func (s *ChargebackStore) Purchase(ctx context.Context, transactionID string) (models.ChargedPurchase, error) {
	var p models.ChargedPurchase
	err := s.conn.QueryRow(ctx, chargedPurchaseSQL, transactionID).Scan(&p.ID, &p.EventTime, &p.AmountMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ChargedPurchase{}, models.ErrPurchaseNotFound
	}
	if err != nil {
		return models.ChargedPurchase{}, fmt.Errorf("read charged purchase: %w", err)
	}
	p.EventTime = p.EventTime.UTC()
	return p, nil
}

const insertChargebackSQL = `
INSERT INTO chargebacks (chargeback_id, purchase_id, reason_code, amount_minor, received_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (chargeback_id) DO NOTHING
RETURNING recorded_at`

// Insert records the chargeback for the purchase. It returns the stored
// chargeback and true when it was recorded now, or the chargeback recorded
// earlier with the same ID and false.
func (s *ChargebackStore) Insert(ctx context.Context, purchaseID int64, c models.Chargeback) (models.StoredChargeback, bool, error) {
	stored := models.StoredChargeback{Chargeback: c}
	err := s.conn.QueryRow(ctx, insertChargebackSQL,
		c.ChargebackID, purchaseID, string(c.Reason), c.AmountMinor, c.ReceivedAt.UTC(),
	).Scan(&stored.RecordedAt)
	if err == nil {
		stored.ReceivedAt, stored.RecordedAt = c.ReceivedAt.UTC(), stored.RecordedAt.UTC()
		return stored, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.StoredChargeback{}, false, fmt.Errorf("insert chargeback: %w", err)
	}
	// The insert met an existing ID. This read is a separate statement, so it
	// also sees a row that a concurrent delivery committed after the insert
	// started.
	existing, err := s.chargeback(ctx, c.ChargebackID)
	return existing, false, err
}

const chargebackSQL = `
SELECT c.chargeback_id, p.transaction_id, c.reason_code, c.amount_minor, c.received_at, c.recorded_at
FROM chargebacks c
JOIN purchases p ON p.id = c.purchase_id
WHERE c.chargeback_id = $1`

func (s *ChargebackStore) chargeback(ctx context.Context, chargebackID string) (models.StoredChargeback, error) {
	var c models.StoredChargeback
	err := s.conn.QueryRow(ctx, chargebackSQL, chargebackID).Scan(
		&c.ChargebackID, &c.TransactionID, &c.Reason, &c.AmountMinor, &c.ReceivedAt, &c.RecordedAt)
	if err != nil {
		return models.StoredChargeback{}, fmt.Errorf("read chargeback: %w", err)
	}
	c.ReceivedAt, c.RecordedAt = c.ReceivedAt.UTC(), c.RecordedAt.UTC()
	return c, nil
}
