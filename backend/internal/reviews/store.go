package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
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
	// ModelVersion is the version of the model that gave ModelScore.
	ModelVersion *string
	FiredRules   []scoring.FiredRule
	DecidedAt    time.Time
}

// CaseItem is a purchase marked for review, with the evidence behind the decision.
type CaseItem struct {
	QueueItem
	// DeviceID is nil when the purchase carried no device ID.
	DeviceID *string
	// RulesetVersion is the ruleset version that scored the purchase.
	RulesetVersion int
}

// ErrNotFound means no purchase under review has the given ID.
var ErrNotFound = errors.New("review case not found")

// DB is a connection that can also start a transaction. Both a pool and a
// transaction qualify, so tests can run the store inside a rolled-back one.
type DB interface {
	db.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Store reads the review queue and saves review decisions.
type Store struct {
	conn DB
	q    *db.Queries
}

func NewStore(conn DB) *Store {
	return &Store{conn: conn, q: db.New(conn)}
}

// resolve saves the decision. When the decision is a saved rejection and
// blockDevice is set, it also blocklists the purchase's device in the same
// transaction. Any other outcome adds no blocklist entry, and a device that is
// already blocked is left as it is.
func (s *Store) resolve(ctx context.Context, purchaseID int64, decision, analystID string, blockDevice bool) (resolution, error) {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return resolution{}, fmt.Errorf("begin review decision: %w", err)
	}
	defer tx.Rollback(ctx)

	row, err := db.New(tx).ResolveReview(ctx, db.ResolveReviewParams{
		TargetPurchaseID: purchaseID,
		Decision:         decision,
		AnalystID:        analystID,
	})
	if err != nil {
		return resolution{}, fmt.Errorf("resolve review: %w", err)
	}
	result := resolution{status: row.Status, resolvedAt: row.ResolvedAt.Time}

	if row.Status == "saved" && decision == "reject" && blockDevice {
		device, err := db.New(tx).GetPurchaseDeviceID(ctx, purchaseID)
		if err != nil {
			return resolution{}, fmt.Errorf("get device of purchase %d: %w", purchaseID, err)
		}
		if device.Valid {
			_, added, err := blocklist.NewStore(tx).AddIfAbsent(ctx, blocklist.Entry{
				Type:   blocklist.TypeDevice,
				Value:  device.String,
				Reason: fmt.Sprintf("Blocked when %s rejected purchase %d", analystID, purchaseID),
			})
			if err != nil {
				return resolution{}, err
			}
			result.deviceBlocked = added
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return resolution{}, fmt.Errorf("commit review decision: %w", err)
	}
	return result, nil
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
		item, err := newQueueItem(r.PurchaseID, r.TransactionID, r.EventTime, r.AmountMinor, r.PlayerID,
			r.RuleScore, r.ModelScore, r.ModelVersion, r.FiredRules, r.DecidedAt)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// Case returns the purchase with the given ID if it is under review. It
// returns ErrNotFound if there is none.
func (s *Store) Case(ctx context.Context, purchaseID int64) (CaseItem, error) {
	r, err := s.q.GetReviewCase(ctx, purchaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CaseItem{}, ErrNotFound
	}
	if err != nil {
		return CaseItem{}, fmt.Errorf("get review case %d: %w", purchaseID, err)
	}
	item, err := newQueueItem(r.PurchaseID, r.TransactionID, r.EventTime, r.AmountMinor, r.PlayerID,
		r.RuleScore, r.ModelScore, r.ModelVersion, r.FiredRules, r.DecidedAt)
	if err != nil {
		return CaseItem{}, err
	}
	reviewCase := CaseItem{QueueItem: item, RulesetVersion: int(r.RulesetVersion)}
	if r.DeviceID.Valid {
		device := r.DeviceID.String
		reviewCase.DeviceID = &device
	}
	return reviewCase, nil
}

func newQueueItem(
	purchaseID int64, transactionID string, eventTime pgtype.Timestamptz, amountMinor int64, playerID string,
	ruleScore int32, modelScore pgtype.Float8, modelVersion pgtype.Text, firedRules []byte, decidedAt pgtype.Timestamptz,
) (QueueItem, error) {
	var fired []scoring.FiredRule
	if err := json.Unmarshal(firedRules, &fired); err != nil {
		return QueueItem{}, fmt.Errorf("decode fired rules of purchase %d: %w", purchaseID, err)
	}
	item := QueueItem{
		PurchaseID:    purchaseID,
		TransactionID: transactionID,
		EventTime:     eventTime.Time.UTC(),
		AmountMinor:   amountMinor,
		PlayerID:      playerID,
		RuleScore:     int(ruleScore),
		FiredRules:    fired,
		DecidedAt:     decidedAt.Time.UTC(),
	}
	if modelScore.Valid {
		score := modelScore.Float64
		item.ModelScore = &score
	}
	if modelVersion.Valid {
		version := modelVersion.String
		item.ModelVersion = &version
	}
	return item, nil
}
