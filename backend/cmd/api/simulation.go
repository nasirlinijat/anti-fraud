package main

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

// newSimulationManager registers no controller or recovery when disabled.
func newSimulationManager(cfg platform.Config, pool, evaluation *pgxpool.Pool) *simulation.Service {
	if !cfg.SimulationEnabled {
		return nil
	}
	setupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &simulator.Client{BaseURL: "http://127.0.0.1:" + cfg.Port, APIKey: cfg.APIKey, HTTP: &http.Client{Timeout: 10 * time.Second}}
	manager, err := simulation.NewService(setupCtx, labels.NewSimulationStore(storage.NewSimulationStore(pool, cfg.DatabaseURL), evaluation), client)
	if err != nil {
		fatal("initialize simulations", err)
	}
	return manager
}
