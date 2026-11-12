package labels

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

// SimulationStore is a simulation store whose runs also keep each purchase's
// private label in the evaluation database. It lives here, not in storage,
// so the scoring path never reaches the labels.
type SimulationStore struct {
	*storage.SimulationStore
	evaluation *pgxpool.Pool
}

func NewSimulationStore(store *storage.SimulationStore, evaluation *pgxpool.Pool) *SimulationStore {
	return &SimulationStore{SimulationStore: store, evaluation: evaluation}
}

// Lock takes the controller lock and returns a controller that keeps labels.
func (s *SimulationStore) Lock(ctx context.Context) (models.SimulationController, bool, error) {
	c, ok, err := s.SimulationStore.Lock(ctx)
	if !ok {
		return nil, false, err
	}
	return &simulationController{SimulationController: c, evaluation: s.evaluation}, true, nil
}

// recoverRunsSQL counts a run's pending purchase from its saved decision, but
// not when its label was rejected or never saved, so that no run counts a
// purchase the evaluation cannot score.
const recoverRunsSQL = storage.RecoverRunsSQL + ` AND NOT EXISTS (SELECT 1 FROM pending_purchase_labels l WHERE l.transaction_id=r.pending_transaction_id AND (l.rejection_reason IS NOT NULL OR l.request_json IS NULL))`

// simulationController settles labels and run progress in one evaluation
// transaction. The embedded controller's connection keeps the lock.
type simulationController struct {
	models.SimulationController
	evaluation *pgxpool.Pool
	current    Label
}

func (c *simulationController) Recover(ctx context.Context) error {
	return pgx.BeginFunc(ctx, c.evaluation, func(tx pgx.Tx) error {
		if _, err := RecoverTx(ctx, tx, ""); err != nil {
			return err
		}
		return storage.RecoverSimulationRunsWith(ctx, tx, recoverRunsSQL)
	})
}

// PrepareLabel saves the purchase's private label as pending and remembers it
// for the next Checkpoint.
func (c *simulationController) PrepareLabel(ctx context.Context, runID string, p simulator.Purchase) error {
	label := FromPurchase(runID, p)
	if err := Prepare(ctx, c.evaluation, label); err != nil {
		return err
	}
	c.current = label
	return nil
}

// RejectLabel marks the purchase's pending label as rejected when the score
// API refused the request, so that the label is never scored.
func (c *simulationController) RejectLabel(ctx context.Context, transactionID string, err error) bool {
	return Reject(ctx, c.evaluation, transactionID, err)
}

// Checkpoint finalizes the remembered label and counts the outcome in the
// same transaction.
func (c *simulationController) Checkpoint(ctx context.Context, runID, transactionID, outcome string) error {
	return Finalize(ctx, c.evaluation, c.current, func(tx pgx.Tx) error {
		return storage.CheckpointSimulation(ctx, tx, runID, transactionID, outcome)
	})
}
