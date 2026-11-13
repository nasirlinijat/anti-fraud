package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// openEvaluationPool keeps reporting optional while requiring healthy private
// persistence before simulation traffic can be dispatched.
func openEvaluationPool(cfg platform.Config) (*pgxpool.Pool, error) {
	if cfg.LabelsDatabaseURL == "" {
		if cfg.SimulationEnabled {
			return nil, errors.New("simulations require evaluation access")
		}
		return nil, nil
	}
	setupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	evaluation, err := platform.NewPool(setupCtx, cfg.LabelsDatabaseURL, 0)
	if err != nil {
		if cfg.SimulationEnabled {
			return nil, errors.New("invalid labels database configuration")
		}
		slog.Warn("quality reporting is unavailable: invalid evaluation configuration")
		return nil, nil
	}
	if cfg.SimulationEnabled {
		if err := labels.Preflight(setupCtx, evaluation); err != nil {
			evaluation.Close()
			return nil, err
		}
	}
	return evaluation, nil
}
