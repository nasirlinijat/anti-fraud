package storage

import (
	"context"
	"fmt"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

const countDecisionsSQL = `
SELECT count(*)
FROM decisions
WHERE decided_at >= $1::timestamptz
  AND decided_at < $2::timestamptz`

const exportDecisionsSQL = `
SELECT d.decided_at, p.transaction_id, p.player_id, p.amount_minor, d.outcome, d.rule_score,
       coalesce((SELECT string_agg(f.rule->>'name', ';' ORDER BY f.position)
                 FROM jsonb_array_elements(d.fired_rules) WITH ORDINALITY AS f(rule, position)), ''),
       d.model_score, d.model_version, d.ruleset_version,
       CASE
         WHEN d.outcome <> 'review' THEN ''
         WHEN r.purchase_id IS NULL THEN 'open'
         WHEN r.decision = 'approve' THEN 'approved'
         ELSE 'rejected'
       END,
       r.analyst_id, r.resolved_at
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
LEFT JOIN review_resolutions r ON r.purchase_id = d.purchase_id
WHERE d.decided_at >= $1::timestamptz
  AND d.decided_at < $2::timestamptz
ORDER BY d.decided_at DESC, d.purchase_id DESC
LIMIT $3`

func (s *StatsStore) CountDecisions(ctx context.Context, window models.StatsWindow) (int64, error) {
	return countDecisions(ctx, s.pool, window)
}

func (s *StatsStore) ExportDecisions(ctx context.Context, window models.StatsWindow, limit int, emit func(models.DecisionExport) error) error {
	return exportDecisions(ctx, s.pool, window, limit, emit)
}

func countDecisions(ctx context.Context, conn platform.DBTX, window models.StatsWindow) (int64, error) {
	var total int64
	if err := conn.QueryRow(ctx, countDecisionsSQL, window.Since, window.Until).Scan(&total); err != nil {
		return 0, fmt.Errorf("count decisions: %w", err)
	}
	return total, nil
}

func exportDecisions(ctx context.Context, conn platform.DBTX, window models.StatsWindow, limit int, emit func(models.DecisionExport) error) error {
	rows, err := conn.Query(ctx, exportDecisionsSQL, window.Since, window.Until, limit)
	if err != nil {
		return fmt.Errorf("export decisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d models.DecisionExport
		if err := rows.Scan(&d.DecidedAt, &d.TransactionID, &d.PlayerID, &d.AmountMinor, &d.Outcome, &d.RuleScore, &d.FiredRules,
			&d.ModelScore, &d.ModelVersion, &d.RulesetVersion, &d.Review, &d.ReviewedBy, &d.ReviewedAt); err != nil {
			return fmt.Errorf("read exported decision: %w", err)
		}
		if err := emit(d); err != nil {
			return err
		}
	}
	return rows.Err()
}
