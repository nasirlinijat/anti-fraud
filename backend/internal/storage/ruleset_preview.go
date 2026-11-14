package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const recentDecisionsSQL = `
SELECT d.purchase_id, p.transaction_id, d.decided_at, d.outcome, d.rule_score, d.fired_rules, d.model_score, d.ruleset_version
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
ORDER BY d.decided_at DESC, d.purchase_id DESC
LIMIT $1`

func (s *RulesetStore) RecentDecisions(ctx context.Context, limit int) ([]models.ReplayDecision, error) {
	rows, err := s.conn.Query(ctx, recentDecisionsSQL, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent decisions: %w", err)
	}
	decisions, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.ReplayDecision, error) {
		var d models.ReplayDecision
		var fired []byte
		if err := row.Scan(&d.PurchaseID, &d.TransactionID, &d.DecidedAt, &d.Outcome, &d.RuleScore, &fired, &d.ModelScore, &d.RulesetVersion); err != nil {
			return d, err
		}
		d.DecidedAt = d.DecidedAt.UTC()
		return d, json.Unmarshal(fired, &d.FiredRules)
	})
	if err != nil {
		return nil, fmt.Errorf("read recent decisions: %w", err)
	}
	return decisions, nil
}
