package reviews

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// QueueItem is a purchase marked for review.
type QueueItem struct {
	PurchaseID    int64
	TransactionID string
	EventTime     time.Time
	AmountMinor   int64
	PlayerID      string
	RuleScore     int
	// ModelScore is nil when the purchase was scored without a model.
	ModelScore *float64
	FiredRules []scoring.FiredRule
	DecidedAt  time.Time
}

// Store reads the review queue.
type Store struct {
	q *db.Queries
}

func NewStore(conn db.DBTX) *Store {
	return &Store{q: db.New(conn)}
}

// Queue returns up to limit purchases marked for review, the earliest decided first.
func (s *Store) Queue(ctx context.Context, limit int) ([]QueueItem, error) {
	return s.queue(ctx, limit, nil)
}

type queueCursor struct {
	decidedAt  time.Time
	purchaseID int64
}

func (s *Store) queue(ctx context.Context, limit int, after *queueCursor) ([]QueueItem, error) {
	params := db.ListReviewQueueParams{PageLimit: int32(limit)}
	if after != nil {
		params.AfterDecidedAt = pgtype.Timestamptz{Time: after.decidedAt, Valid: true}
		params.AfterPurchaseID = pgtype.Int8{Int64: after.purchaseID, Valid: true}
	}
	rows, err := s.q.ListReviewQueue(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list review queue: %w", err)
	}

	items := make([]QueueItem, 0, len(rows))
	for _, r := range rows {
		var fired []scoring.FiredRule
		if err := json.Unmarshal(r.FiredRules, &fired); err != nil {
			return nil, fmt.Errorf("decode fired rules of purchase %d: %w", r.PurchaseID, err)
		}
		item := QueueItem{
			PurchaseID:    r.PurchaseID,
			TransactionID: r.TransactionID,
			EventTime:     r.EventTime.Time.UTC(),
			AmountMinor:   r.AmountMinor,
			PlayerID:      r.PlayerID,
			RuleScore:     int(r.RuleScore),
			FiredRules:    fired,
			DecidedAt:     r.DecidedAt.Time.UTC(),
		}
		if r.ModelScore.Valid {
			score := r.ModelScore.Float64
			item.ModelScore = &score
		}
		items = append(items, item)
	}
	return items, nil
}
