package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const casePlayerPurchaseLimit = 5

// ReviewStore reads the review queue and saves review decisions.
type ReviewStore struct {
	conn Conn
}

func NewReviewStore(conn Conn) *ReviewStore {
	return &ReviewStore{conn: conn}
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

// Resolve saves the decision. When the decision is a saved rejection and
// blockDevice is set, it also blocklists the purchase's device in the same
// transaction. Any other outcome adds no blocklist entry, and a device that is
// already blocked is left as it is.
func (s *ReviewStore) Resolve(ctx context.Context, purchaseID int64, decision, analystID string, blockDevice bool) (models.Resolution, error) {
	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return models.Resolution{}, fmt.Errorf("begin review decision: %w", err)
	}
	defer tx.Rollback(ctx)

	var result models.Resolution
	var resolvedAt *time.Time
	if err := tx.QueryRow(ctx, resolveReview, purchaseID, decision, analystID).Scan(&result.Status, &resolvedAt); err != nil {
		return models.Resolution{}, fmt.Errorf("resolve review: %w", err)
	}
	if resolvedAt != nil {
		result.ResolvedAt = *resolvedAt
	}

	if result.Status == models.ResolutionSaved && decision == "reject" && blockDevice {
		var device *string
		if err := tx.QueryRow(ctx, getPurchaseDeviceID, purchaseID).Scan(&device); err != nil {
			return models.Resolution{}, fmt.Errorf("get device of purchase %d: %w", purchaseID, err)
		}
		if device != nil {
			_, added, err := blocklist.NewStore(tx).AddIfAbsent(ctx, blocklist.Entry{
				Type:   blocklist.TypeDevice,
				Value:  *device,
				Reason: fmt.Sprintf("Blocked when %s rejected purchase %d", analystID, purchaseID),
			}, analystID)
			if err != nil {
				return models.Resolution{}, err
			}
			result.DeviceBlocked = added
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return models.Resolution{}, fmt.Errorf("commit review decision: %w", err)
	}
	return result, nil
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

// Queue returns up to limit open reviews in the given order, after the cursor
// when it is set.
func (s *ReviewStore) Queue(ctx context.Context, limit int, order models.QueueOrder, after *models.QueueCursor) ([]models.QueueItem, error) {
	var score any
	var decidedAt *time.Time
	var purchaseID *int64
	if after != nil {
		decidedAt, purchaseID = &after.DecidedAt, &after.PurchaseID
		score = after.ScoreKey
		if order == models.QueueByRuleScore {
			score = int32(after.ScoreKey)
		}
	}
	query := listQueueByModelScore
	if order == models.QueueByRuleScore {
		query = listQueueByRuleScore
	}
	rows, err := s.conn.Query(ctx, query, score, decidedAt, purchaseID, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list review queue: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.QueueItem, error) {
		return scanQueueItem(row)
	})
	if err != nil {
		return nil, fmt.Errorf("list review queue: %w", err)
	}
	return items, nil
}

// scanQueueItem reads the queueItemColumns, followed by any extra
// destinations.
func scanQueueItem(row pgx.Row, extra ...any) (models.QueueItem, error) {
	var item models.QueueItem
	var firedRules []byte
	dest := append([]any{
		&item.PurchaseID, &item.TransactionID, &item.EventTime, &item.AmountMinor, &item.PlayerID, &item.DeviceID,
		&item.RuleScore, &item.ModelScore, &item.ModelVersion, &firedRules, &item.DecidedAt,
	}, extra...)
	if err := row.Scan(dest...); err != nil {
		return models.QueueItem{}, err
	}
	if err := json.Unmarshal(firedRules, &item.FiredRules); err != nil {
		return models.QueueItem{}, fmt.Errorf("decode fired rules of purchase %d: %w", item.PurchaseID, err)
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
// returns models.ErrReviewNotFound if there is none.
func (s *ReviewStore) Case(ctx context.Context, purchaseID int64) (models.CaseItem, error) {
	var c models.CaseItem
	item, err := scanQueueItem(s.conn.QueryRow(ctx, getReviewCase, purchaseID), &c.RulesetVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CaseItem{}, models.ErrReviewNotFound
	}
	if err != nil {
		return models.CaseItem{}, fmt.Errorf("get review case %d: %w", purchaseID, err)
	}
	c.QueueItem = item

	rows, err := s.conn.Query(ctx, listCasePlayerPurchases, item.PlayerID, item.EventTime, item.TransactionID, int32(casePlayerPurchaseLimit))
	if err != nil {
		return models.CaseItem{}, fmt.Errorf("list player purchases for case %d: %w", purchaseID, err)
	}
	c.PlayerPurchases, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.PlayerPurchase, error) {
		var p models.PlayerPurchase
		err := row.Scan(&p.TransactionID, &p.EventTime, &p.AmountMinor, &p.Outcome)
		p.EventTime = p.EventTime.UTC()
		return p, err
	})
	if err != nil {
		return models.CaseItem{}, fmt.Errorf("list player purchases for case %d: %w", purchaseID, err)
	}

	if item.DeviceID != nil {
		var a models.DeviceActivity
		err := s.conn.QueryRow(ctx, getCaseDeviceActivity, *item.DeviceID, item.EventTime, item.TransactionID).
			Scan(&a.Purchases, &a.DistinctCards, &a.LastSeenAt)
		if err != nil {
			return models.CaseItem{}, fmt.Errorf("get device activity for case %d: %w", purchaseID, err)
		}
		a.LastSeenAt = a.LastSeenAt.UTC()
		c.DeviceActivity = &a
	}
	return c, nil
}
