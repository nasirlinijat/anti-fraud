package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

const simulationColumns = `id, settings, status, created_at, finished_at, sent, failed, allowed, reviewed, blocked, pending_transaction_id IS NOT NULL, error`

// simulationRunByRequestSQL finds the run an operator started with a request ID.
const simulationRunByRequestSQL = `SELECT ` + simulationColumns + ` FROM simulation_runs WHERE operator_id=$1 AND request_id=$2`

const checkpointSimulationSQL = `UPDATE simulation_runs SET sent=sent+1,allowed=allowed+($3='allow')::int,reviewed=reviewed+($3='review')::int,blocked=blocked+($3='block')::int,pending_transaction_id=NULL WHERE id=$1 AND pending_transaction_id=$2`

func scanSimulationRun(row pgx.Row) (models.SimulationRun, error) {
	var r models.SimulationRun
	var settings []byte
	err := row.Scan(&r.ID, &settings, &r.Status, &r.CreatedAt, &r.FinishedAt, &r.Sent, &r.Failed, &r.Allowed, &r.Reviewed, &r.Blocked, &r.Pending, &r.Error)
	if err == nil {
		err = json.Unmarshal(settings, &r.Settings)
	}
	r.CreatedAt = r.CreatedAt.UTC()
	if r.FinishedAt != nil {
		v := r.FinishedAt.UTC()
		r.FinishedAt = &v
	}
	return r, err
}

// findSimulationByRequest returns the run of an earlier start with the same
// request ID, and false when there is none.
func findSimulationByRequest(ctx context.Context, conn platform.DBTX, operatorID, requestID string) (models.SimulationRun, bool, error) {
	run, err := scanSimulationRun(conn.QueryRow(ctx, simulationRunByRequestSQL, operatorID, requestID))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.SimulationRun{}, false, nil
	}
	return run, err == nil, err
}

// SimulationStore reads and writes simulation runs. Writes of a running
// simulation go through the SimulationController that holds the lock.
type SimulationStore struct {
	pool *pgxpool.Pool
	dsn  string
}

// NewSimulationStore returns the store. dsn opens the dedicated controller
// connections.
func NewSimulationStore(pool *pgxpool.Pool, dsn string) *SimulationStore {
	return &SimulationStore{pool: pool, dsn: dsn}
}

// Lock takes the simulation controller lock on a dedicated connection, because
// a pool release must never retain a session lock. It returns false when
// another controller holds the lock.
func (s *SimulationStore) Lock(ctx context.Context) (models.SimulationController, bool, error) {
	c, err := pgx.Connect(ctx, s.dsn)
	if err != nil {
		return nil, false, err
	}
	var ok bool
	err = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(27149,77)`).Scan(&ok)
	if err != nil || !ok {
		_ = c.Close(ctx)
		return nil, false, err
	}
	return &SimulationController{conn: c}, true, nil
}

func (s *SimulationStore) Get(ctx context.Context, id string) (models.SimulationRun, error) {
	run, err := scanSimulationRun(s.pool.QueryRow(ctx, `SELECT `+simulationColumns+` FROM simulation_runs WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.SimulationRun{}, models.ErrSimulationNotFound
	}
	return run, err
}

func (s *SimulationStore) FindByRequest(ctx context.Context, operatorID, requestID string) (models.SimulationRun, bool, error) {
	return findSimulationByRequest(ctx, s.pool, operatorID, requestID)
}

// Active returns the run that is starting, running, or stopping.
func (s *SimulationStore) Active(ctx context.Context) (models.SimulationRun, error) {
	return scanSimulationRun(s.pool.QueryRow(ctx, `SELECT `+simulationColumns+` FROM simulation_runs WHERE status IN ('starting','running','stopping')`))
}

// List returns up to limit runs, newest first, created before the run with
// the ID before. An empty before starts at the newest run. An unknown before
// returns models.ErrInvalidSimulationCursor.
func (s *SimulationStore) List(ctx context.Context, before string, limit int) ([]models.SimulationRun, error) {
	var createdAt time.Time
	if before != "" {
		err := s.pool.QueryRow(ctx, `SELECT created_at FROM simulation_runs WHERE id=$1`, before).Scan(&createdAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, models.ErrInvalidSimulationCursor
		}
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT `+simulationColumns+` FROM simulation_runs WHERE $1='' OR (created_at,id)<($2,$1) ORDER BY created_at DESC,id DESC LIMIT $3`, before, createdAt, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []models.SimulationRun{}
	for rows.Next() {
		run, err := scanSimulationRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

// RequestStop marks a starting or running run as stopping. It returns false
// when the run was in no such state.
func (s *SimulationStore) RequestStop(ctx context.Context, id, operatorID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE simulation_runs SET status='stopping',stop_requested_by=$2 WHERE id=$1 AND status IN ('starting','running')`, id, operatorID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RecoverSimulationRuns settles the runs a previous controller left behind. A
// single sequential worker writes its pending ID before sending, so the commit
// and checkpoint gap is recovered by exact transaction lookup, never by a
// prefix scan. Unfinished runs become interrupted.
func RecoverSimulationRuns(ctx context.Context, conn platform.DBTX) error {
	return RecoverSimulationRunsWith(ctx, conn, RecoverRunsSQL)
}

// RecoverRunsSQL counts the pending purchase of each run from its saved
// decision. A caller that keeps more state per purchase can narrow it with an
// extra condition and pass the result to RecoverSimulationRunsWith.
const RecoverRunsSQL = `UPDATE simulation_runs r SET
 sent=r.sent+1, allowed=r.allowed+(d.outcome='allow')::int,
 reviewed=r.reviewed+(d.outcome='review')::int, blocked=r.blocked+(d.outcome='block')::int,
 pending_transaction_id=NULL, failed=GREATEST(r.failed-1,0)
 FROM purchases p JOIN decisions d ON d.purchase_id=p.id
 WHERE p.transaction_id=r.pending_transaction_id`

// RecoverSimulationRunsWith settles the runs like RecoverSimulationRuns, with
// countSQL in place of RecoverRunsSQL to count each run's pending purchase.
func RecoverSimulationRunsWith(ctx context.Context, conn platform.DBTX, countSQL string) error {
	_, err := conn.Exec(ctx, countSQL)
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, `UPDATE simulation_runs SET status='interrupted', finished_at=now(), error='backend interrupted the run'
 WHERE status IN ('starting','running','stopping')`)
	return err
}

// CheckpointSimulation counts the outcome of the run's pending transaction.
func CheckpointSimulation(ctx context.Context, conn platform.DBTX, runID, transactionID, outcome string) error {
	_, err := conn.Exec(ctx, checkpointSimulationSQL, runID, transactionID, outcome)
	return err
}

// SimulationController holds the controller lock on its own connection and
// checkpoints each sequential purchase of the run it drives.
type SimulationController struct {
	conn *pgx.Conn
}

func (c *SimulationController) Close() {
	_ = c.conn.Close(context.Background())
}

// Recover settles the runs a previous controller left behind in one
// transaction on the controller connection.
func (c *SimulationController) Recover(ctx context.Context) error {
	return pgx.BeginFunc(ctx, c.conn, func(tx pgx.Tx) error {
		return RecoverSimulationRuns(ctx, tx)
	})
}

func (c *SimulationController) FindByRequest(ctx context.Context, operatorID, requestID string) (models.SimulationRun, bool, error) {
	return findSimulationByRequest(ctx, c.conn, operatorID, requestID)
}

func (c *SimulationController) Create(ctx context.Context, id, operatorID string, s models.SimulationSettings) (models.SimulationRun, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return models.SimulationRun{}, err
	}
	return scanSimulationRun(c.conn.QueryRow(ctx, `INSERT INTO simulation_runs(id,operator_id,request_id,settings,status) VALUES($1,$2,$3,$4,'starting') RETURNING `+simulationColumns, id, operatorID, s.RequestID, body))
}

// MarkPending saves the transaction ID about to be sent. It returns false
// when the run is no longer starting or running.
func (c *SimulationController) MarkPending(ctx context.Context, runID, transactionID string) (bool, error) {
	tag, err := c.conn.Exec(ctx, `UPDATE simulation_runs SET pending_transaction_id=$2,status='running' WHERE id=$1 AND status IN ('starting','running')`, runID, transactionID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PrepareLabel does nothing: this controller keeps no labels.
func (c *SimulationController) PrepareLabel(context.Context, string, simulator.Purchase) error {
	return nil
}

// RejectLabel does nothing: this controller keeps no labels.
func (c *SimulationController) RejectLabel(context.Context, string, error) bool {
	return false
}

// OutcomeOf returns the saved decision of a transaction.
func (c *SimulationController) OutcomeOf(ctx context.Context, transactionID string) (string, error) {
	var outcome string
	err := c.conn.QueryRow(ctx, `SELECT d.outcome FROM purchases p JOIN decisions d ON d.purchase_id=p.id WHERE p.transaction_id=$1`, transactionID).Scan(&outcome)
	return outcome, err
}

// Checkpoint counts the outcome of the run's pending transaction.
func (c *SimulationController) Checkpoint(ctx context.Context, runID, transactionID, outcome string) error {
	return CheckpointSimulation(ctx, c.conn, runID, transactionID, outcome)
}

// Finish saves the final status of a run that is still active. A stop request
// turns completed into stopped.
func (c *SimulationController) Finish(ctx context.Context, runID, status, message string) error {
	_, err := c.conn.Exec(ctx, `UPDATE simulation_runs SET status=CASE WHEN status='stopping' AND $2='completed' THEN 'stopped' ELSE $2 END,finished_at=now(),error=$3,failed=failed+($2='failed')::int WHERE id=$1 AND status IN ('starting','running','stopping')`, runID, status, message)
	return err
}
