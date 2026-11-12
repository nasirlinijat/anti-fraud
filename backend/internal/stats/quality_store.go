package stats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// QualityStore uses the separate evaluation pool, never the scoring connection.
type QualityStore struct{ pool *pgxpool.Pool }

func NewQualityStore(evaluation *pgxpool.Pool) *QualityStore { return &QualityStore{pool: evaluation} }

// QualityMetricsSQL aggregates only finalized answers in one source and run.
const QualityMetricsSQL = `
SELECT count(*) FILTER (WHERE l.is_fraud AND d.outcome IN ('review','block')),
 count(*) FILTER (WHERE NOT l.is_fraud AND d.outcome IN ('review','block')),
 count(*) FILTER (WHERE NOT l.is_fraud AND d.outcome='allow'),
 count(*) FILTER (WHERE l.is_fraud AND d.outcome='allow'),
 coalesce(sum(p.amount_minor) FILTER (WHERE l.is_fraud AND d.outcome IN ('review','block')),0)::text,
 coalesce(sum(p.amount_minor) FILTER (WHERE l.is_fraud AND d.outcome='allow'),0)::text
FROM purchase_labels l JOIN purchases p USING(transaction_id)
JOIN decisions d ON d.purchase_id=p.id WHERE l.source=$1 AND l.run_id=$2`

// QualityPendingSQL excludes intents already finalized by an explicit import.
const QualityPendingSQL = `
SELECT count(*) FILTER (WHERE l.rejection_reason IS NULL AND l.request_json IS NOT NULL),
 count(*) FILTER (WHERE l.rejection_reason IS NOT NULL OR l.request_json IS NULL)
FROM pending_purchase_labels l WHERE l.source=$1 AND l.run_id=$2
 AND NOT EXISTS (SELECT 1 FROM purchase_labels f WHERE f.transaction_id=l.transaction_id
 AND f.source=l.source AND f.run_id=l.run_id)`

const qualityConsoleSQL = `SELECT sent,status FROM simulation_runs WHERE $1='simulator' AND id=$2`

// QualityRunsSQL includes CLI, imported, pending-only and legacy console runs.
const QualityRunsSQL = `
SELECT run_id FROM (
 SELECT run_id FROM purchase_labels WHERE source=$1 AND run_id>$2
 UNION SELECT run_id FROM pending_purchase_labels WHERE source=$1 AND run_id>$2
 UNION SELECT id FROM simulation_runs WHERE $1='simulator' AND id>$2
) runs ORDER BY run_id LIMIT $3`

// Quality reads counts and coverage from one read-only snapshot.
func (s *QualityStore) Quality(ctx context.Context, source, run string) (Quality, error) {
	if s.pool == nil {
		return Quality{}, ErrQualityUnavailable
	}
	q := Quality{Source: source, RunID: run}
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return readQuality(ctx, tx, &q)
	})
	if err != nil {
		return Quality{}, err
	}
	q.calculateRates()
	q.GeneratedAt = time.Now().UTC()
	return q, nil
}

func readQuality(ctx context.Context, conn platform.DBTX, q *Quality) error {
	if err := conn.QueryRow(ctx, QualityMetricsSQL, q.Source, q.RunID).Scan(
		&q.TruePositives, &q.FalsePositives, &q.TrueNegatives, &q.FalseNegatives,
		&q.FraudValueFlaggedMinor, &q.FraudValueAllowedMinor,
	); err != nil {
		return fmt.Errorf("read quality metrics: %w", err)
	}
	if err := conn.QueryRow(ctx, QualityPendingSQL, q.Source, q.RunID).Scan(&q.Coverage.Pending, &q.Coverage.Quarantined); err != nil {
		return fmt.Errorf("read quality pending coverage: %w", err)
	}
	return readQualityConsole(ctx, conn, q)
}

func readQualityConsole(ctx context.Context, conn platform.DBTX, q *Quality) error {
	var sent int64
	var status string
	err := conn.QueryRow(ctx, qualityConsoleSQL, q.Source, q.RunID).Scan(&sent, &status)
	if err == nil {
		q.Coverage.ScoredDecisions, q.Coverage.RunStatus = &sent, &status
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read quality console coverage: %w", err)
	}
	if q.TruePositives+q.FalsePositives+q.TrueNegatives+q.FalseNegatives+q.Coverage.Pending+q.Coverage.Quarantined == 0 {
		return ErrQualityRunNotFound
	}
	return nil
}

// Runs lists stable run identities without exposing individual purchase answers.
func (s *QualityStore) Runs(ctx context.Context, source, after string, limit int) (QualityRuns, error) {
	page := QualityRuns{Source: source, RunIDs: []string{}}
	if s.pool == nil {
		return page, ErrQualityUnavailable
	}
	rows, err := s.pool.Query(ctx, QualityRunsSQL, source, after, limit+1)
	if err != nil {
		return page, fmt.Errorf("list quality runs: %w", err)
	}
	page.RunIDs, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return page, fmt.Errorf("read quality run IDs: %w", err)
	}
	if len(page.RunIDs) > limit {
		page.RunIDs = page.RunIDs[:limit]
		page.NextCursor = page.RunIDs[limit-1]
	}
	return page, nil
}
