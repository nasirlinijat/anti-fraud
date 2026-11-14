package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// DecisionStatusSQL reads one committed snapshot using the existing unique keys.
const DecisionStatusSQL = `
SELECT p.transaction_id, d.outcome, r.decision, r.resolved_at
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
LEFT JOIN review_resolutions r ON r.purchase_id = p.id AND d.outcome = 'review'
WHERE p.transaction_id = $1`

// DecisionStatusStore reads minimal status without touching scoring or evidence.
type DecisionStatusStore struct{ conn platform.DBTX }

func NewDecisionStatusStore(conn platform.DBTX) *DecisionStatusStore {
	return &DecisionStatusStore{conn: conn}
}

// Find returns ErrPurchaseNotFound when no scored purchase has this exact ID.
func (s *DecisionStatusStore) Find(ctx context.Context, transactionID string) (models.SavedPurchaseStatus, error) {
	var saved models.SavedPurchaseStatus
	err := s.conn.QueryRow(ctx, DecisionStatusSQL, transactionID).Scan(&saved.TransactionID, &saved.Outcome, &saved.ReviewDecision, &saved.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.SavedPurchaseStatus{}, models.ErrPurchaseNotFound
	}
	if err != nil {
		return models.SavedPurchaseStatus{}, fmt.Errorf("read decision status: %w", err)
	}
	return saved, nil
}
