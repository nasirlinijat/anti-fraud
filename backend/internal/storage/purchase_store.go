package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// Conn runs queries and starts transactions. *pgxpool.Pool and pgx.Tx implement it.
type Conn interface {
	platform.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PurchaseStore saves scored purchases and their decisions, and reads purchase history.
type PurchaseStore struct {
	conn Conn
}

func NewPurchaseStore(conn Conn) *PurchaseStore {
	return &PurchaseStore{conn: conn}
}

const cardVelocityRange = `
FROM purchases
WHERE card_token = $1
  AND event_time > $2::timestamptz
  AND event_time <= $3::timestamptz`

// CardPurchasesLastHourSQL uses one snapshot for the count and the bounded set of IDs.
const CardPurchasesLastHourSQL = `
SELECT counted.uses,
       CASE WHEN counted.uses BETWEEN 1 AND $4::bigint
            THEN ARRAY(SELECT id ` + cardVelocityRange + ` LIMIT $4)
            ELSE ARRAY[]::bigint[] END
FROM (SELECT count(*) AS uses ` + cardVelocityRange + `) counted`

// CardPurchasesLastHour counts saved purchases with the card in the hour
// through at. The IDs are returned only when the complete set fits.
func (s *PurchaseStore) CardPurchasesLastHour(ctx context.Context, cardToken string, at time.Time) (models.CardVelocityHistory, error) {
	var history models.CardVelocityHistory
	start := at.Add(-scoring.CardVelocityWindowMinutes * time.Minute)
	if err := s.conn.QueryRow(ctx, CardPurchasesLastHourSQL, cardToken, start.UTC(), at.UTC(), int32(models.CardVelocityReferenceLimit)).
		Scan(&history.Count, &history.PurchaseIDs); err != nil {
		return models.CardVelocityHistory{}, fmt.Errorf("read card velocity history: %w", err)
	}
	if len(history.PurchaseIDs) != history.Count {
		history.PurchaseIDs = nil
	}
	return history, nil
}

// CountDeviceCardsLastHourSQL counts the other cards used on the device in the
// hour before $2, plus one for the current card.
const CountDeviceCardsLastHourSQL = `
SELECT (count(DISTINCT card_token) + 1)::bigint AS card_count
FROM purchases
WHERE device_id IS NOT NULL
  AND device_id = $1::text
  AND event_time > $2::timestamptz - interval '1 hour'
  AND event_time <= $2::timestamptz
  AND card_token <> $3::text`

// DeviceCardsLastHour counts distinct cards used on a device, including the current card.
func (s *PurchaseStore) DeviceCardsLastHour(ctx context.Context, deviceID, cardToken string, at time.Time) (int64, error) {
	var n int64
	if err := s.conn.QueryRow(ctx, CountDeviceCardsLastHourSQL, deviceID, at.UTC(), cardToken).Scan(&n); err != nil {
		return 0, fmt.Errorf("count distinct device cards: %w", err)
	}
	return n, nil
}

// TrustedPurchaseHistorySQL summarizes the player's allowed or approved purchases
// between $3 and the given purchase. The total is text, so it cannot overflow.
const TrustedPurchaseHistorySQL = `
SELECT
    count(*) AS trusted_count,
    coalesce(sum(p.amount_minor), 0)::text AS total_minor,
    coalesce(bool_or(p.device_id = $1::text), false)::boolean AS device_seen
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
WHERE p.player_id = $2::text
  AND p.event_time > $3::timestamptz
  AND (p.event_time, p.transaction_id) < ($4::timestamptz, $5::text)
  AND ` + trustedDecision

// trustedDecision is true when the decision d of purchase p allowed it, or a
// review approved it. Blocked, rejected, and undecided purchases are not
// trusted.
const trustedDecision = `CASE d.outcome
      WHEN 'allow' THEN true
      WHEN 'review' THEN (
          SELECT r.decision = 'approve'
          FROM review_resolutions r
          WHERE r.purchase_id = p.id
      )
      ELSE false
  END`

// TrustedHistory reads earlier allowed or approved purchases for a request with a device ID.
func (s *PurchaseStore) TrustedHistory(ctx context.Context, p models.Purchase, windowDays int32) (*scoring.TrustedPurchaseHistory, error) {
	since := p.EventTime.Add(-time.Duration(windowDays) * 24 * time.Hour)
	var h scoring.TrustedPurchaseHistory
	var totalMinor string
	err := s.conn.QueryRow(ctx, TrustedPurchaseHistorySQL,
		*p.DeviceID, p.PlayerID, since.UTC(), p.EventTime.UTC(), p.TransactionID,
	).Scan(&h.Count, &totalMinor, &h.DeviceSeen)
	if err != nil {
		return nil, fmt.Errorf("read trusted purchase history: %w", err)
	}
	total, ok := new(big.Int).SetString(totalMinor, 10)
	if !ok {
		return nil, errors.New("invalid trusted purchase total")
	}
	h.TotalMinor = total
	return &h, nil
}

// FeatureHistory reads the purchase history the model features need.
func (s *PurchaseStore) FeatureHistory(ctx context.Context, in features.Input) (features.History, error) {
	return features.NewStore(s.conn).History(ctx, in)
}

// PlayerEarlierModelScoresSQL counts and sums the unblended model scores of
// the player's purchases before the given one.
const PlayerEarlierModelScoresSQL = `
SELECT
    count(d.model_score_unblended) AS scored,
    coalesce(sum(d.model_score_unblended ORDER BY p.event_time, p.transaction_id), 0)::float8 AS score_sum
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
WHERE p.player_id = $1
  AND (p.event_time, p.transaction_id) < ($2::timestamptz, $3::text)`

// PlayerEarlierModelScores counts and sums the unblended model scores of the
// player's purchases before the given one.
func (s *PurchaseStore) PlayerEarlierModelScores(ctx context.Context, playerID string, at time.Time, transactionID string) (float64, int64, error) {
	var count int64
	var sum float64
	if err := s.conn.QueryRow(ctx, PlayerEarlierModelScoresSQL, playerID, at.UTC(), transactionID).Scan(&count, &sum); err != nil {
		return 0, 0, fmt.Errorf("read earlier model scores: %w", err)
	}
	return sum, count, nil
}

// savedScore returns the decision saved for transaction ID $1, and whether the
// saved purchase has the same fields as $1 to $28, which bind purchaseArgs.
const savedScore = `
SELECT
    ROW(
        p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.card_token,
        p.card_issuer, p.card_network, p.card_type, p.card_country, p.card_category,
        p.product_code, p.billing_region, p.billing_country, p.billing_distance,
        p.email_domain, p.recipient_email_domain, p.device_type, p.device_name, p.device_id,
        p.ip_address, p.ip_country, p.account_created_at,
        p.client_os, p.client_browser, p.screen_resolution,
        p.cvv_result, p.avs_result, p.three_ds_result
    ) IS NOT DISTINCT FROM ROW(
        $1::text, $2::timestamptz, $3::bigint,
        $4::text, $5::text,
        $6::text, $7::text, $8::text,
        $9::text, $10::text, $11::text,
        $12::text, $13::text, $14::float8,
        $15::text, $16::text,
        $17::text, $18::text, $19::text,
        $20::inet, $21::text, $22::timestamptz,
        $23::text, $24::text, $25::text,
        $26::text, $27::text, $28::text
    ) AS request_matches,
    d.outcome, d.rule_score, d.fired_rules, d.ruleset_version, d.model_score, d.model_version
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
WHERE p.transaction_id = $1`

// FindSavedDecision returns nil when the transaction has no saved decision.
// It returns models.ErrDuplicateTransaction if the saved purchase differs.
func (s *PurchaseStore) FindSavedDecision(ctx context.Context, p models.Purchase) (*models.Decision, error) {
	var matches bool
	var firedRules []byte
	var d models.Decision
	err := s.conn.QueryRow(ctx, savedScore, purchaseArgs(p)...).Scan(
		&matches, &d.Result.Outcome, &d.Result.Score, &firedRules, &d.RulesetVersion, &d.ModelScore, &d.ModelVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read saved decision: %w", err)
	}
	if !matches {
		return nil, models.ErrDuplicateTransaction
	}
	if err := json.Unmarshal(firedRules, &d.Result.FiredRules); err != nil {
		return nil, fmt.Errorf("decode saved fired rules: %w", err)
	}
	return &d, nil
}

const insertPurchase = `
INSERT INTO purchases (
    transaction_id, event_time, amount_minor, player_id, card_token,
    card_issuer, card_network, card_type, card_country, card_category,
    product_code, billing_region, billing_country, billing_distance,
    email_domain, recipient_email_domain, device_type, device_name, device_id,
    ip_address, ip_country, account_created_at,
    client_os, client_browser, screen_resolution,
    cvv_result, avs_result, three_ds_result
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17, $18, $19,
    $20, $21, $22,
    $23, $24, $25,
    $26, $27, $28
)
RETURNING id`

const insertDecision = `
INSERT INTO decisions (
    purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, model_score,
    model_score_unblended, scoring_latency_us, model_version, ip_intel, card_velocity_evidence
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12
)`

// Save stores a purchase and its decision in one transaction and returns the
// purchase ID. It returns models.ErrDuplicateTransaction if the transaction ID is already saved.
func (s *PurchaseStore) Save(ctx context.Context, p models.Purchase, d models.Decision) (int64, error) {
	firedRules := d.Result.FiredRules
	if firedRules == nil {
		firedRules = []scoring.FiredRule{}
	}
	firedJSON, err := json.Marshal(firedRules)
	if err != nil {
		return 0, fmt.Errorf("encode fired rules: %w", err)
	}

	inputs := d.ModelInputs
	if inputs == nil {
		inputs = map[string]float64{}
	}
	inputsJSON, err := json.Marshal(inputs)
	if err != nil {
		return 0, fmt.Errorf("encode model inputs: %w", err)
	}
	ipIntelJSON, err := ipIntelJSON(d.IPIntel)
	if err != nil {
		return 0, err
	}

	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var id int64
	if err := tx.QueryRow(ctx, insertPurchase, purchaseArgs(p)...).Scan(&id); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "purchases_transaction_id_key" {
			return 0, models.ErrDuplicateTransaction
		}
		return 0, fmt.Errorf("insert purchase %s: %w", p.TransactionID, err)
	}

	_, err = tx.Exec(ctx, insertDecision,
		id, int32(d.RulesetVersion), string(d.Result.Outcome), int32(d.Result.Score), firedJSON, inputsJSON,
		d.ModelScore, d.ModelScoreUnblended, d.Latency.Microseconds(), d.ModelVersion, ipIntelJSON, d.CardVelocity,
	)
	if err != nil {
		return 0, fmt.Errorf("insert decision for purchase %s: %w", p.TransactionID, err)
	}
	if err := features.AddToCounts(ctx, tx, p.FeatureInput(), d.FeatureHistory); err != nil {
		return 0, fmt.Errorf("purchase %s: %w", p.TransactionID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit purchase %s: %w", p.TransactionID, err)
	}
	return id, nil
}

// purchaseArgs binds the purchase fields for insertPurchase and savedScore, in
// column order. Nil optional fields bind NULL.
func purchaseArgs(p models.Purchase) []any {
	var accountCreatedAt *time.Time
	if p.AccountCreatedAt != nil {
		utc := p.AccountCreatedAt.UTC()
		accountCreatedAt = &utc
	}
	return []any{
		p.TransactionID, p.EventTime.UTC(), p.AmountMinor, p.PlayerID, p.CardToken,
		p.CardIssuer, p.CardNetwork, p.CardType, p.CardCountry, p.CardCategory,
		p.ProductCode, p.BillingRegion, p.BillingCountry, p.BillingDistance,
		p.EmailDomain, p.RecipientEmailDomain, p.DeviceType, p.DeviceName, p.DeviceID,
		p.IPAddress, p.IPCountry, accountCreatedAt,
		p.ClientOS, p.ClientBrowser, p.ScreenResolution,
		p.CVVResult, p.AVSResult, p.ThreeDSResult,
	}
}

// ipIntelJSON encodes the lookup result, or returns nil to save NULL when no
// lookup was made.
func ipIntelJSON(r *ipintel.Result) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode IP intelligence: %w", err)
	}
	return data, nil
}

const uniqueViolation = "23505"
