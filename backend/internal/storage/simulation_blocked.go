package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

// runBlockedPurchasesSQL pages through a run's blocked purchases, newest
// first. $1 and $2 bound the run's transaction ID prefix, $3 is the purchase
// ID of the last item already returned, or 0 for the first page, and $4 is
// the page size.
const runBlockedPurchasesSQL = `
SELECT p.id, p.transaction_id, p.player_id, p.amount_minor, p.event_time,
       p.card_country, p.ip_country, p.device_id,
       d.rule_score, d.ruleset_version, rs.block_threshold, d.fired_rules, d.decided_at
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
JOIN rulesets rs ON rs.version = d.ruleset_version
WHERE p.transaction_id ~>=~ $1 AND p.transaction_id ~<~ $2
  AND d.outcome = 'block'
  AND ($3::bigint = 0 OR p.id < $3::bigint)
ORDER BY p.id DESC
LIMIT $4`

// BlockedPurchases returns up to limit purchases of the run that scoring
// blocked, newest first, with purchase IDs below beforeID, or from the
// newest when beforeID is 0. It leaves Reason empty.
func (s *SimulationStore) BlockedPurchases(ctx context.Context, runID string, beforeID int64, limit int) ([]models.BlockedPurchase, error) {
	from, to := runTransactionRange(runID)
	rows, err := s.pool.Query(ctx, runBlockedPurchasesSQL, from, to, beforeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list blocked purchases: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanBlockedPurchase)
	if err != nil {
		return nil, fmt.Errorf("read blocked purchases: %w", err)
	}
	return items, nil
}

// runTransactionRange returns the bounds of the run's transaction IDs, which
// the simulator writes as "<run ID>-txn-<n>". The upper bound replaces the
// final "-" with the next character, ".".
func runTransactionRange(runID string) (string, string) {
	return runID + "-txn-", runID + "-txn."
}

func scanBlockedPurchase(row pgx.CollectableRow) (models.BlockedPurchase, error) {
	var b models.BlockedPurchase
	var firedRules []byte
	err := row.Scan(&b.PurchaseID, &b.TransactionID, &b.PlayerID, &b.AmountMinor, &b.EventTime,
		&b.CardCountry, &b.IPCountry, &b.DeviceID,
		&b.RuleScore, &b.RulesetVersion, &b.BlockThreshold, &firedRules, &b.DecidedAt)
	if err != nil {
		return b, err
	}
	b.EventTime, b.DecidedAt = b.EventTime.UTC(), b.DecidedAt.UTC()
	return b, json.Unmarshal(firedRules, &b.FiredRules)
}
