package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

const casePlayerPurchaseLimit = 5

// A review case shows the player's account events from this many days before
// the purchase, at most caseAccountEventLimit of them.
const (
	caseAccountEventDays  = 30
	caseAccountEventLimit = 20
)

// BlocklistRefresher refreshes a blocklist value after its change commits.
type BlocklistRefresher interface {
	Refresh(ctx context.Context, t models.BlocklistType, value string)
}

// ReviewStore reads the review queue and saves review decisions.
type ReviewStore struct {
	conn      Conn
	refresher BlocklistRefresher
}

func NewReviewStore(conn Conn) *ReviewStore {
	return &ReviewStore{conn: conn}
}

// UseBlocklistCache registers a cache to refresh when a review decision
// blocklists a device, card or player.
func (s *ReviewStore) UseBlocklistCache(r BlocklistRefresher) *ReviewStore {
	s.refresher = r
	return s
}

// resolveReview saves a decision for a purchase still awaiting review and
// reports what happened: saved, not_found, not_review, already_resolved, or
// not_claimed. The decision is saved only while its analyst
// holds the claim on the purchase.
//
// A review purchase that was not saved although its claim is held lost the
// insert to a decision committed meanwhile, which the statement snapshot
// cannot see, so it is reported as already_resolved.
const resolveReview = `
WITH candidate AS (
    SELECT d.purchase_id, d.outcome
    FROM decisions d
    WHERE d.purchase_id = $1
), claim AS (
    SELECT EXISTS (
        SELECT 1
        FROM review_claims c
        JOIN accounts a ON a.id = c.account_id
        WHERE c.purchase_id = $1 AND a.username = $3::text
    ) AS held
), saved AS (
    INSERT INTO review_resolutions (purchase_id, decision, analyst_id)
    SELECT candidate.purchase_id, $2::text, $3::text
    FROM candidate, claim
    WHERE candidate.outcome = 'review' AND claim.held
    ON CONFLICT (purchase_id) DO NOTHING
    RETURNING resolved_at
)
SELECT CASE
    WHEN EXISTS (SELECT 1 FROM saved) THEN 'saved'
    WHEN NOT EXISTS (SELECT 1 FROM candidate) THEN 'not_found'
    WHEN NOT EXISTS (SELECT 1 FROM candidate WHERE outcome = 'review') THEN 'not_review'
    WHEN (SELECT held FROM claim) THEN 'already_resolved'
    WHEN EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = $1) THEN 'already_resolved'
    ELSE 'not_claimed'
END::text AS status,
(SELECT resolved_at FROM saved)::timestamptz AS resolved_at`

const getPurchaseBlockValues = `SELECT device_id, card_token, player_id FROM purchases WHERE id = $1`

// Resolve saves the decision. When the decision is a saved rejection, it also
// blocklists the purchase's device, card and player that d.Blocks asks for, in
// the same transaction. Any other outcome adds no blocklist entry, and a value
// that is already blocked is left as it is.
func (s *ReviewStore) Resolve(ctx context.Context, d models.ReviewDecision) (models.Resolution, error) {
	tx, err := s.beginReview(ctx, d.PurchaseID)
	if err != nil {
		return models.Resolution{}, fmt.Errorf("begin review decision: %w", err)
	}
	defer tx.Rollback(ctx)

	result, err := saveResolution(ctx, tx, d)
	if err != nil {
		return models.Resolution{}, err
	}
	var added []models.BlocklistEntry
	if result.Status == models.ResolutionSaved && d.Decision == "reject" && d.Blocks.Any() {
		if added, err = blockPurchaseValues(ctx, tx, d); err != nil {
			return models.Resolution{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Resolution{}, fmt.Errorf("commit review decision: %w", err)
	}
	result.Blocked = blockedValues(added)
	s.refreshBlocklist(ctx, added)
	return result, nil
}

func saveResolution(ctx context.Context, tx pgx.Tx, d models.ReviewDecision) (models.Resolution, error) {
	var result models.Resolution
	var resolvedAt *time.Time
	err := tx.QueryRow(ctx, resolveReview, d.PurchaseID, d.Decision, d.AnalystID).Scan(&result.Status, &resolvedAt)
	if err != nil {
		return models.Resolution{}, fmt.Errorf("resolve review: %w", err)
	}
	if resolvedAt != nil {
		result.ResolvedAt = *resolvedAt
	}
	return result, nil
}

// blockPurchaseValues adds the requested values of the purchase to the
// blocklist and returns the entries it added.
func blockPurchaseValues(ctx context.Context, tx pgx.Tx, d models.ReviewDecision) ([]models.BlocklistEntry, error) {
	values, err := purchaseBlockValues(ctx, tx, d.PurchaseID)
	if err != nil {
		return nil, err
	}
	blocklist := NewBlocklistStore(tx)
	var added []models.BlocklistEntry
	for _, entry := range requestedEntries(d, values) {
		_, isNew, err := blocklist.AddIfAbsent(ctx, entry, d.AnalystID)
		if err != nil {
			return nil, err
		}
		if isNew {
			added = append(added, entry)
		}
	}
	return added, nil
}

// blockValues are the values of a purchase that a rejection can blocklist.
type blockValues struct {
	// DeviceID is nil when the purchase carried no device ID.
	DeviceID  *string
	CardToken string
	PlayerID  string
}

func purchaseBlockValues(ctx context.Context, tx pgx.Tx, purchaseID int64) (blockValues, error) {
	var v blockValues
	if err := tx.QueryRow(ctx, getPurchaseBlockValues, purchaseID).Scan(&v.DeviceID, &v.CardToken, &v.PlayerID); err != nil {
		return blockValues{}, fmt.Errorf("get blocklist values of purchase %d: %w", purchaseID, err)
	}
	return v, nil
}

// requestedEntries lists the entries the decision asks for. A purchase
// without a device has no device entry.
func requestedEntries(d models.ReviewDecision, v blockValues) []models.BlocklistEntry {
	reason := fmt.Sprintf("Blocked when %s rejected purchase %d", d.AnalystID, d.PurchaseID)
	var entries []models.BlocklistEntry
	if d.Blocks.Device && v.DeviceID != nil {
		entries = append(entries, models.BlocklistEntry{Type: models.BlocklistDevice, Value: *v.DeviceID, Reason: reason})
	}
	if d.Blocks.Card {
		entries = append(entries, models.BlocklistEntry{Type: models.BlocklistCard, Value: v.CardToken, Reason: reason})
	}
	if d.Blocks.Player {
		entries = append(entries, models.BlocklistEntry{Type: models.BlocklistPlayer, Value: v.PlayerID, Reason: reason})
	}
	return entries
}

func blockedValues(added []models.BlocklistEntry) models.ReviewBlocks {
	var blocked models.ReviewBlocks
	for _, entry := range added {
		switch entry.Type {
		case models.BlocklistDevice:
			blocked.Device = true
		case models.BlocklistCard:
			blocked.Card = true
		case models.BlocklistPlayer:
			blocked.Player = true
		}
	}
	return blocked
}

// refreshBlocklist runs only after the commit, so that the cache never holds
// an entry that was rolled back.
func (s *ReviewStore) refreshBlocklist(ctx context.Context, added []models.BlocklistEntry) {
	if s.refresher == nil {
		return
	}
	for _, entry := range added {
		s.refresher.Refresh(ctx, entry.Type, entry.Value)
	}
}

// queueItemColumns are the columns scanQueueItem reads, in order. They need
// the queueItemTables.
const queueItemColumns = `d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.device_id,
       d.rule_score, d.model_score, d.model_version, d.fired_rules, d.decided_at, ca.username, rc.claimed_at`

const queueItemTables = `decisions d
JOIN purchases p ON p.id = d.purchase_id
LEFT JOIN review_claims rc ON rc.purchase_id = d.purchase_id
LEFT JOIN accounts ca ON ca.id = rc.account_id`

const openReviewFilter = `
WHERE d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)`

const queueClaimFilter = ` AND ($5::text IS NULL OR ca.username = $5::text)`

// listQueueByRuleScore pages through open reviews, highest rule score first.
// $1 to $3 are the cursor of the last item already returned, or NULL for the
// first page. $5 limits the page to purchases claimed by that username, or is
// NULL for all of them.
const listQueueByRuleScore = `
SELECT ` + queueItemColumns + `
FROM ` + queueItemTables + openReviewFilter + queueClaimFilter + `
  AND (
      $1::integer IS NULL
      OR (-d.rule_score, d.decided_at, d.purchase_id) > (
          $1::integer, $2::timestamptz, $3::bigint
      )
  )
ORDER BY -d.rule_score, d.decided_at, d.purchase_id
LIMIT $4 OFFSET $6`

// listQueueByModelScore pages through open reviews, highest model score first
// and purchases without a model score last. The parameters are those of
// listQueueByRuleScore.
const listQueueByModelScore = `
SELECT ` + queueItemColumns + `
FROM ` + queueItemTables + openReviewFilter + queueClaimFilter + `
  AND (
      $1::double precision IS NULL
      OR (COALESCE(-d.model_score, 1::double precision), d.decided_at, d.purchase_id) > (
          $1::double precision, $2::timestamptz, $3::bigint
      )
  )
ORDER BY COALESCE(-d.model_score, 1::double precision), d.decided_at, d.purchase_id
LIMIT $4 OFFSET $6`

// Queue returns up to limit open reviews in the given order, after the cursor
// when it is set. When claimedBy is set, it returns only the purchases claimed
// by that username.
func (s *ReviewStore) Queue(ctx context.Context, limit int, order models.QueueOrder, after *models.QueueCursor, claimedBy *string) ([]models.QueueItem, error) {
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
	return readQueueRows(ctx, s.conn, queueSQL(order), score, decidedAt, purchaseID, int32(limit), claimedBy, int64(0))
}

func queueSQL(order models.QueueOrder) string {
	if order == models.QueueByRuleScore {
		return listQueueByRuleScore
	}
	return listQueueByModelScore
}

func readQueueRows(ctx context.Context, conn platform.DBTX, query string, args ...any) ([]models.QueueItem, error) {
	rows, err := conn.Query(ctx, query, args...)
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
		&item.RuleScore, &item.ModelScore, &item.ModelVersion, &firedRules, &item.DecidedAt, &item.ClaimedBy, &item.ClaimedAt,
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
	if item.ClaimedAt != nil {
		claimedAt := item.ClaimedAt.UTC()
		item.ClaimedAt = &claimedAt
	}
	return item, nil
}

const caseEvidenceColumns = queueItemColumns + `, d.ruleset_version, p.cvv_result, p.avs_result, p.three_ds_result, d.ip_intel, d.model_inputs`

// getReviewCase returns a purchase that is still awaiting review.
const getReviewCase = `
SELECT ` + caseEvidenceColumns + `
FROM ` + queueItemTables + `
WHERE d.purchase_id = $1
  AND d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)`

// getResolvedReviewCase returns the same evidence and the saved analyst decision.
const getResolvedReviewCase = `
SELECT ` + caseEvidenceColumns + `, r.decision, r.analyst_id, r.resolved_at
FROM ` + queueItemTables + `
JOIN review_resolutions r ON r.purchase_id = d.purchase_id
WHERE d.purchase_id = $1 AND d.outcome = 'review'`

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

// listCaseAccountEvents lists the player's account events in the 30 days
// before the purchase, newest first.
const listCaseAccountEvents = `
SELECT event_id, player_id, event_type, event_time, device_id, ip_country, received_at
FROM account_events
WHERE player_id = $1
  AND event_time > $2::timestamptz - make_interval(days => $3)
  AND event_time < $2::timestamptz
ORDER BY event_time DESC, event_id DESC
LIMIT $4`

// Case returns the purchase with the given ID if it is under review. It
// returns models.ErrReviewNotFound if there is none.
func (s *ReviewStore) Case(ctx context.Context, purchaseID int64) (models.CaseItem, error) {
	return s.readCase(ctx, s.conn.QueryRow(ctx, getReviewCase, purchaseID), nil)
}

// ResolvedCase returns the evidence and analyst decision for a resolved review.
func (s *ReviewStore) ResolvedCase(ctx context.Context, purchaseID int64) (models.CaseItem, error) {
	return s.readCase(ctx, s.conn.QueryRow(ctx, getResolvedReviewCase, purchaseID), &models.CaseResolution{})
}

func (s *ReviewStore) readCase(ctx context.Context, row pgx.Row, resolution *models.CaseResolution) (models.CaseItem, error) {
	c := models.CaseItem{Resolution: resolution}
	var ipIntel, modelInputs []byte
	dest := []any{&c.RulesetVersion, &c.CVVResult, &c.AVSResult, &c.ThreeDSResult, &ipIntel, &modelInputs}
	if resolution != nil {
		dest = append(dest, &resolution.Decision, &resolution.AnalystID, &resolution.ResolvedAt)
	}
	item, err := scanQueueItem(row, dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CaseItem{}, models.ErrReviewNotFound
	}
	if err != nil {
		return models.CaseItem{}, fmt.Errorf("get review case: %w", err)
	}
	c.QueueItem = item
	if resolution != nil {
		resolution.ResolvedAt = resolution.ResolvedAt.UTC()
	}
	if ipIntel != nil {
		if err := json.Unmarshal(ipIntel, &c.IPIntel); err != nil {
			return models.CaseItem{}, fmt.Errorf("decode IP intelligence of case %d: %w", item.PurchaseID, err)
		}
	}
	if err := json.Unmarshal(modelInputs, &c.ModelInputs); err != nil {
		return models.CaseItem{}, fmt.Errorf("decode model inputs of case %d: %w", item.PurchaseID, err)
	}
	if c.PlayerPurchases, err = s.casePlayerPurchases(ctx, item); err != nil {
		return models.CaseItem{}, fmt.Errorf("list player purchases for case %d: %w", item.PurchaseID, err)
	}
	if c.DeviceActivity, err = s.caseDeviceActivity(ctx, item); err != nil {
		return models.CaseItem{}, fmt.Errorf("get device activity for case %d: %w", item.PurchaseID, err)
	}
	if c.AccountEvents, err = s.caseAccountEvents(ctx, item); err != nil {
		return models.CaseItem{}, fmt.Errorf("list account events for case %d: %w", item.PurchaseID, err)
	}
	return c, nil
}

func (s *ReviewStore) casePlayerPurchases(ctx context.Context, item models.QueueItem) ([]models.PlayerPurchase, error) {
	rows, err := s.conn.Query(ctx, listCasePlayerPurchases, item.PlayerID, item.EventTime, item.TransactionID, int32(casePlayerPurchaseLimit))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.PlayerPurchase, error) {
		var p models.PlayerPurchase
		err := row.Scan(&p.TransactionID, &p.EventTime, &p.AmountMinor, &p.Outcome)
		p.EventTime = p.EventTime.UTC()
		return p, err
	})
}

// caseDeviceActivity returns nil for a purchase without a device ID.
func (s *ReviewStore) caseDeviceActivity(ctx context.Context, item models.QueueItem) (*models.DeviceActivity, error) {
	if item.DeviceID == nil {
		return nil, nil
	}
	var a models.DeviceActivity
	err := s.conn.QueryRow(ctx, getCaseDeviceActivity, *item.DeviceID, item.EventTime, item.TransactionID).
		Scan(&a.Purchases, &a.DistinctCards, &a.LastSeenAt)
	if err != nil {
		return nil, err
	}
	a.LastSeenAt = a.LastSeenAt.UTC()
	return &a, nil
}

func (s *ReviewStore) caseAccountEvents(ctx context.Context, item models.QueueItem) ([]models.AccountEvent, error) {
	rows, err := s.conn.Query(ctx, listCaseAccountEvents,
		item.PlayerID, item.EventTime, int32(caseAccountEventDays), int32(caseAccountEventLimit))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.AccountEvent, error) {
		var e models.AccountEvent
		err := row.Scan(&e.EventID, &e.PlayerID, &e.EventType, &e.EventTime, &e.DeviceID, &e.IPCountry, &e.ReceivedAt)
		e.EventTime = e.EventTime.UTC()
		e.ReceivedAt = e.ReceivedAt.UTC()
		return e, err
	})
}

// claimReview gives an open review to the username unless another account
// holds it, and reports what happened: claimed, not_found, not_review,
// already_resolved, or held. For a new claim it also returns the claim time.
//
// The statement snapshot cannot see a claim committed by another request
// meanwhile, so the holder of an existing claim is read by a new statement.
const claimReview = `
WITH candidate AS (
    SELECT d.purchase_id, d.outcome,
           EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id) AS resolved
    FROM decisions d
    WHERE d.purchase_id = $1
), claimant AS (
    SELECT id FROM accounts WHERE username = $2::text AND disabled_at IS NULL
), saved AS (
    INSERT INTO review_claims (purchase_id, account_id)
    SELECT candidate.purchase_id, claimant.id
    FROM candidate, claimant
    WHERE candidate.outcome = 'review' AND NOT candidate.resolved
    ON CONFLICT (purchase_id) DO NOTHING
    RETURNING claimed_at
)
SELECT CASE
    WHEN EXISTS (SELECT 1 FROM saved) THEN 'claimed'
    WHEN NOT EXISTS (SELECT 1 FROM candidate) THEN 'not_found'
    WHEN NOT EXISTS (SELECT 1 FROM candidate WHERE outcome = 'review') THEN 'not_review'
    WHEN EXISTS (SELECT 1 FROM candidate WHERE resolved) THEN 'already_resolved'
    ELSE 'held'
END::text AS status,
(SELECT claimed_at FROM saved)::timestamptz AS claimed_at`

const getReviewClaim = `
SELECT a.username, c.claimed_at
FROM review_claims c
JOIN accounts a ON a.id = c.account_id
WHERE c.purchase_id = $1`

// Claim gives the purchase under review to the username, unless another
// account already holds it. Claiming a purchase the username already holds
// reports ClaimAlreadyYours with the original claim time.
func (s *ReviewStore) Claim(ctx context.Context, purchaseID int64, username string) (models.Claim, error) {
	tx, err := s.beginReview(ctx, purchaseID)
	if err != nil {
		return models.Claim{}, err
	}
	defer tx.Rollback(ctx)
	claim, err := claimPurchase(ctx, tx, purchaseID, username)
	if err != nil {
		return models.Claim{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.Claim{}, fmt.Errorf("commit review claim: %w", err)
	}
	return claim, nil
}

func claimPurchase(ctx context.Context, tx pgx.Tx, purchaseID int64, username string) (models.Claim, error) {
	var status string
	var claimedAt *time.Time
	if err := tx.QueryRow(ctx, claimReview, purchaseID, username).Scan(&status, &claimedAt); err != nil {
		return models.Claim{}, fmt.Errorf("claim review %d: %w", purchaseID, err)
	}
	if claimedAt != nil {
		return models.Claim{Status: models.ClaimSaved, ClaimedBy: username, ClaimedAt: claimedAt.UTC()}, nil
	}
	if status != "held" {
		return models.Claim{Status: models.ClaimStatus(status)}, nil
	}
	return heldClaim(ctx, tx, purchaseID, username)
}

// heldClaim reads the claim that stopped a new one.
func heldClaim(ctx context.Context, tx pgx.Tx, purchaseID int64, username string) (models.Claim, error) {
	claim := models.Claim{Status: models.ClaimHeldByOther}
	err := tx.QueryRow(ctx, getReviewClaim, purchaseID).Scan(&claim.ClaimedBy, &claim.ClaimedAt)
	if err != nil {
		return models.Claim{}, fmt.Errorf("get claim of review %d: %w", purchaseID, err)
	}
	if claim.ClaimedBy == username {
		claim.Status = models.ClaimAlreadyYours
	}
	claim.ClaimedAt = claim.ClaimedAt.UTC()
	return claim, nil
}
