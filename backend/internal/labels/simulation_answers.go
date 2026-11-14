package labels

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// SimulationAnswers reads the console comparison through the evaluation pool.
type SimulationAnswers struct{ pool *pgxpool.Pool }

func NewSimulationAnswers(pool *pgxpool.Pool) *SimulationAnswers {
	return &SimulationAnswers{pool: pool}
}

// ErrSimulationAnswersUnavailable means no evaluation database was configured.
var ErrSimulationAnswersUnavailable = errors.New("simulation answers unavailable")

// SimulationAnswerPage contains saved decisions and their finalized synthetic answers.
type SimulationAnswerPage struct {
	Items      []SimulationAnswer `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

// SimulationAnswer identifies one saved purchase without exposing its private scenario.
type SimulationAnswer struct {
	PurchaseID    string          `json:"purchase_id"`
	TransactionID string          `json:"transaction_id"`
	Outcome       scoring.Outcome `json:"outcome"`
	Answer        string          `json:"answer"`
}

// SimulationAnswerQuery selects one run's page of decisions.
type SimulationAnswerQuery struct {
	RunID      string
	Before     int64
	Limit      int
	Exceptions bool
}

// The prefix identifies purchases before their labels finalize, including older
// console runs. The exact numeric suffix excludes lookalike run prefixes.
const simulationAnswersSQL = `
SELECT p.id, p.transaction_id, d.outcome,
       CASE l.is_fraud WHEN true THEN 'fraud' WHEN false THEN 'legitimate' ELSE 'unknown' END
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
LEFT JOIN purchase_labels l ON l.transaction_id = p.transaction_id
    AND l.source = 'simulator' AND l.run_id = $1
WHERE p.transaction_id ~>=~ $2 AND p.transaction_id ~<~ $3
  AND substr(p.transaction_id, length($2) + 1) ~ '^[0-9]+$'
  AND ($4::bigint = 0 OR p.id < $4)
  AND (NOT $5::boolean OR
       (l.is_fraud IS TRUE AND d.outcome = 'allow') OR
       (l.is_fraud IS FALSE AND d.outcome IN ('review', 'block')))
ORDER BY p.id DESC
LIMIT $6`

// Page reads only saved decisions, with filtering applied before pagination.
func (s *SimulationAnswers) Page(ctx context.Context, q SimulationAnswerQuery) (SimulationAnswerPage, error) {
	if s.pool == nil {
		return SimulationAnswerPage{}, ErrSimulationAnswersUnavailable
	}
	var page SimulationAnswerPage
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if err := requireConsoleRun(ctx, tx, q.RunID); err != nil {
			return err
		}
		var err error
		page, err = readSimulationAnswers(ctx, tx, q)
		return err
	})
	return page, err
}

func requireConsoleRun(ctx context.Context, tx pgx.Tx, runID string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM simulation_runs WHERE id = $1)`, runID).Scan(&exists); err != nil {
		return fmt.Errorf("check simulation run: %w", err)
	}
	if !exists {
		return models.ErrSimulationNotFound
	}
	return nil
}

func readSimulationAnswers(ctx context.Context, tx pgx.Tx, q SimulationAnswerQuery) (SimulationAnswerPage, error) {
	rows, err := tx.Query(ctx, simulationAnswersSQL, q.RunID, q.RunID+"-txn-", q.RunID+"-txn.", q.Before, q.Exceptions, q.Limit+1)
	if err != nil {
		return SimulationAnswerPage{}, fmt.Errorf("list simulation answers: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanSimulationAnswer)
	if err != nil {
		return SimulationAnswerPage{}, fmt.Errorf("read simulation answers: %w", err)
	}
	page := SimulationAnswerPage{Items: items}
	if len(items) > q.Limit {
		page.Items = items[:q.Limit]
		page.NextCursor = page.Items[q.Limit-1].PurchaseID
	}
	return page, nil
}

func scanSimulationAnswer(row pgx.CollectableRow) (SimulationAnswer, error) {
	var item SimulationAnswer
	var id int64
	err := row.Scan(&id, &item.TransactionID, &item.Outcome, &item.Answer)
	item.PurchaseID = strconv.FormatInt(id, 10)
	return item, err
}
