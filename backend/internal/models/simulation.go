package models

import (
	"context"
	"errors"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

var (
	ErrSimulationNotFound      = errors.New("simulation not found")
	ErrInvalidSimulationCursor = errors.New("invalid history cursor")
)

// SimulationSettings is the normalized, immutable request used for start retries.
type SimulationSettings struct {
	RequestID string              `json:"request_id"`
	Mode      string              `json:"mode"`
	Scenarios []simulator.Pattern `json:"scenarios"`
	Seed      uint64              `json:"seed"`
	Rate      float64             `json:"rate"`
	Count     int                 `json:"count"`
}

// SimulationRun is a durable snapshot. Pending indicates an outcome not yet checkpointed.
type SimulationRun struct {
	ID         string             `json:"id"`
	Settings   SimulationSettings `json:"settings"`
	Status     string             `json:"status"`
	CreatedAt  time.Time          `json:"created_at"`
	FinishedAt *time.Time         `json:"finished_at"`
	Sent       int64              `json:"sent"`
	Failed     int64              `json:"failed"`
	Allowed    int64              `json:"allowed"`
	Reviewed   int64              `json:"reviewed"`
	Blocked    int64              `json:"blocked"`
	Pending    bool               `json:"pending"`
	Error      string             `json:"error,omitempty"`
}

// SimulationController is a database session that holds the simulation
// controller lock. Only the holder starts runs and writes their progress.
type SimulationController interface {
	Recover(ctx context.Context) error
	FindByRequest(ctx context.Context, operatorID, requestID string) (SimulationRun, bool, error)
	Create(ctx context.Context, id, operatorID string, s SimulationSettings) (SimulationRun, error)
	MarkPending(ctx context.Context, runID, transactionID string) (bool, error)
	PrepareLabel(ctx context.Context, runID string, p simulator.Purchase) error
	// RejectLabel marks the pending label of a purchase the API refused, and
	// reports whether the error was such a refusal.
	RejectLabel(ctx context.Context, transactionID string, err error) bool
	OutcomeOf(ctx context.Context, transactionID string) (string, error)
	Checkpoint(ctx context.Context, runID, transactionID, outcome string) error
	Finish(ctx context.Context, runID, status, message string) error
	Close()
}
