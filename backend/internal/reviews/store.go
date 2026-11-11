package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// QueueItem is a purchase marked for review.
type QueueItem struct {
	PurchaseID    int64
	TransactionID string
	EventTime     time.Time
	AmountMinor   int64
	PlayerID      string
	// DeviceID is nil when the purchase carried no device ID.
	DeviceID  *string
	RuleScore int
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
	// RulesetVersion is the ruleset version that scored the purchase.
	RulesetVersion int
	// PlayerPurchases lists the player's recent purchases before this one, newest first.
	PlayerPurchases []PlayerPurchase
	// DeviceActivity summarizes activity on the same device, or is nil when the purchase had no device ID.
	DeviceActivity *DeviceActivity
}

// PlayerPurchase is an earlier purchase by the same player.
type PlayerPurchase struct {
	TransactionID string
	EventTime     time.Time
	AmountMinor   int64
	Outcome       scoring.Outcome
}

// DeviceActivity summarizes recent activity on the same device.
type DeviceActivity struct {
	Purchases     int64
	DistinctCards int64
	LastSeenAt    time.Time
}

const casePlayerPurchaseLimit = 5

// ErrNotFound means no purchase under review has the given ID.
var ErrNotFound = errors.New("review case not found")

// DB is a connection that can also start a transaction. Both a pool and a
// transaction qualify, so tests can run the store inside a rolled-back one.
type DB interface {
	platform.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Store reads the review queue and saves review decisions.
type Store struct {
	conn DB
}

func NewStore(conn DB) *Store {
	return &Store{conn: conn}
}

// resolveReview saves a decision for a purchase still awaiting review and
// reports what happened: saved, not_found, already_resolved, or not_review.
const resolveReview = `
WITH candidate AS (
    SELECT d.purchase_id, d.outcome
    FROM decisions d
    WHERE d.purchase_id = $1
), saved AS (
    INSERT INTO review_resolutions (purchase_id, decision, analyst_id)
    SELECT candidate.purchase_id, $2::text, $3::text
    FROM candidate
    WHERE candidate.outcome = 'review'
    ON CONFLICT (purchase_id) DO NOTHING
    RETURNING resolved_at
)
SELECT CASE
    WHEN EXISTS (SELECT 1 FROM saved) THEN 'saved'
    WHEN NOT EXISTS (SELECT 1 FROM candidate) THEN 'not_found'
    WHEN EXISTS (SELECT 1 FROM candidate WHERE outcome = 'review') THEN 'already_resolved'
    ELSE 'not_review'
END::text AS status,
(SELECT resolved_at FROM saved)::timestamptz AS resolved_at`

const getPurchaseDeviceID = `SELECT device_id FROM purchases WHERE id = $1`

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

	var result resolution
	var resolvedAt *time.Time
	if err := tx.QueryRow(ctx, resolveReview, purchaseID, decision, analystID).Scan(&result.status, &resolvedAt); err != nil {
		return resolution{}, fmt.Errorf("resolve review: %w", err)
	}
	if resolvedAt != nil {
		result.resolvedAt = *resolvedAt
	}

	if result.status == "saved" && decision == "reject" && blockDevice {
		var device *string
		if err := tx.QueryRow(ctx, getPurchaseDeviceID, purchaseID).Scan(&device); err != nil {
			return resolution{}, fmt.Errorf("get device of purchase %d: %w", purchaseID, err)
		}
		if device != nil {
			_, added, err := blocklist.NewStore(tx).AddIfAbsent(ctx, blocklist.Entry{
				Type:   blocklist.TypeDevice,
				Value:  *device,
				Reason: fmt.Sprintf("Blocked when %s rejected purchase %d", analystID, purchaseID),
			}, analystID)
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

// Queue returns up to limit purchases marked for review, highest rule score first.
func (s *Store) Queue(ctx context.Context, limit int) ([]QueueItem, error) {
	return s.queue(ctx, limit, byRuleScore, nil)
}

type queueOrder string

const (
	byRuleScore  queueOrder = "rule_score"
	byModelScore queueOrder = "model_score"
)

type queueCursor struct {
	decidedAt  time.Time
	purchaseID int64
	scoreKey   float64
}

func cursorFor(order queueOrder, item QueueItem) queueCursor {
	// Match the ascending score keys in the SQL, including the key for NULL.
	key := -float64(item.RuleScore)
	if order == byModelScore {
		key = 1
		if item.ModelScore != nil {
			key = -*item.ModelScore
		}
	}
	return queueCursor{decidedAt: item.DecidedAt, purchaseID: item.PurchaseID, scoreKey: key}
}

// queueItemColumns are the columns scanQueueItem reads, in order.
const queueItemColumns = `d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.device_id,
       d.rule_score, d.model_score, d.model_version, d.fired_rules, d.decided_at`

// listQueueByRuleScore pages through open reviews, highest rule score first.
// $1 to $3 are the cursor of the last item already returned, or NULL for the
// first page.
const listQueueByRuleScore = `
SELECT ` + queueItemColumns + `
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)
  AND (
      $1::integer IS NULL
      OR (-d.rule_score, d.decided_at, d.purchase_id) > (
          $1::integer, $2::timestamptz, $3::bigint
      )
  )
ORDER BY -d.rule_score, d.decided_at, d.purchase_id
LIMIT $4`

// listQueueByModelScore pages through open reviews, highest model score first
// and purchases without a model score last.
const listQueueByModelScore = `
SELECT ` + queueItemColumns + `
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)
  AND (
      $1::double precision IS NULL
      OR (COALESCE(-d.model_score, 1::double precision), d.decided_at, d.purchase_id) > (
          $1::double precision, $2::timestamptz, $3::bigint
      )
  )
ORDER BY COALESCE(-d.model_score, 1::double precision), d.decided_at, d.purchase_id
LIMIT $4`

func (s *Store) queue(ctx context.Context, limit int, order queueOrder, after *queueCursor) ([]QueueItem, error) {
	var score any
	var decidedAt *time.Time
	var purchaseID *int64
	if after != nil {
		decidedAt, purchaseID = &after.decidedAt, &after.purchaseID
		score = after.scoreKey
		if order == byRuleScore {
			score = int32(after.scoreKey)
		}
	}
	query := listQueueByModelScore
	if order == byRuleScore {
		query = listQueueByRuleScore
	}
	rows, err := s.conn.Query(ctx, query, score, decidedAt, purchaseID, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list review queue: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (QueueItem, error) {
		return scanQueueItem(row)
	})
	if err != nil {
		return nil, fmt.Errorf("list review queue: %w", err)
	}
	return items, nil
}

// scanQueueItem reads the queueItemColumns, followed by any extra
// destinations.
func scanQueueItem(row pgx.Row, extra ...any) (QueueItem, error) {
	var item QueueItem
	var firedRules []byte
	dest := append([]any{
		&item.PurchaseID, &item.TransactionID, &item.EventTime, &item.AmountMinor, &item.PlayerID, &item.DeviceID,
		&item.RuleScore, &item.ModelScore, &item.ModelVersion, &firedRules, &item.DecidedAt,
	}, extra...)
	if err := row.Scan(dest...); err != nil {
		return QueueItem{}, err
	}
	if err := json.Unmarshal(firedRules, &item.FiredRules); err != nil {
		return QueueItem{}, fmt.Errorf("decode fired rules of purchase %d: %w", item.PurchaseID, err)
	}
	// Decisions saved before rule messages existed show the rule name.
	for i := range item.FiredRules {
		if item.FiredRules[i].Message == "" {
			item.FiredRules[i].Message = string(item.FiredRules[i].Name)
		}
	}
	item.EventTime = item.EventTime.UTC()
	item.DecidedAt = item.DecidedAt.UTC()
	return item, nil
}

// getReviewCase returns a purchase that is still awaiting review.
const getReviewCase = `
SELECT ` + queueItemColumns + `, d.ruleset_version
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.purchase_id = $1
  AND d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)`

// listCasePlayerPurchases lists the player's purchases before the given one,
// newest first.
const listCasePlayerPurchases = `
SELECT p.transaction_id, p.event_time, p.amount_minor, d.outcome
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
WHERE p.player_id = $1
  AND (p.event_time, p.transaction_id) < ($2::timestamptz, $3::text)
ORDER BY p.event_time DESC, p.transaction_id DESC
LIMIT $4`

// getCaseDeviceActivity summarizes the device's purchases up to and including
// the given one.
const getCaseDeviceActivity = `
SELECT
    count(*) AS purchases,
    count(DISTINCT card_token) AS distinct_cards,
    max(event_time)::timestamptz AS last_seen_at
FROM purchases
WHERE device_id IS NOT NULL
  AND device_id = $1::text
  AND (event_time, transaction_id) <= ($2::timestamptz, $3::text)`

// Case returns the purchase with the given ID if it is under review. It
// returns ErrNotFound if there is none.
func (s *Store) Case(ctx context.Context, purchaseID int64) (CaseItem, error) {
	var c CaseItem
	item, err := scanQueueItem(s.conn.QueryRow(ctx, getReviewCase, purchaseID), &c.RulesetVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return CaseItem{}, ErrNotFound
	}
	if err != nil {
		return CaseItem{}, fmt.Errorf("get review case %d: %w", purchaseID, err)
	}
	c.QueueItem = item

	rows, err := s.conn.Query(ctx, listCasePlayerPurchases, item.PlayerID, item.EventTime, item.TransactionID, int32(casePlayerPurchaseLimit))
	if err != nil {
		return CaseItem{}, fmt.Errorf("list player purchases for case %d: %w", purchaseID, err)
	}
	c.PlayerPurchases, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (PlayerPurchase, error) {
		var p PlayerPurchase
		err := row.Scan(&p.TransactionID, &p.EventTime, &p.AmountMinor, &p.Outcome)
		p.EventTime = p.EventTime.UTC()
		return p, err
	})
	if err != nil {
		return CaseItem{}, fmt.Errorf("list player purchases for case %d: %w", purchaseID, err)
	}

	if item.DeviceID != nil {
		var a DeviceActivity
		err := s.conn.QueryRow(ctx, getCaseDeviceActivity, *item.DeviceID, item.EventTime, item.TransactionID).
			Scan(&a.Purchases, &a.DistinctCards, &a.LastSeenAt)
		if err != nil {
			return CaseItem{}, fmt.Errorf("get device activity for case %d: %w", purchaseID, err)
		}
		a.LastSeenAt = a.LastSeenAt.UTC()
		c.DeviceActivity = &a
	}
	return c, nil
}
