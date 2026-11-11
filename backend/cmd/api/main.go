package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/reviews"
	"github.com/nasirlinijat/anti-fraud/backend/internal/rulesets"
	"github.com/nasirlinijat/anti-fraud/backend/internal/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulations"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"github.com/nasirlinijat/anti-fraud/backend/internal/stats"
)

func main() {
	cfg, err := platform.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	pool, err := platform.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("create database pool: %v", err)
	}
	defer pool.Close()

	mux := http.NewServeMux()
	platform.RegisterHealthRoutes(mux, pool)
	rulesetStore := rulesets.NewStore(pool)
	rulesets.RegisterRoutes(mux, rulesetStore, cfg.APIKey, cfg.OperatorKeys)
	handler := score.NewHandler(rulesetStore, blocklist.NewStore(pool), score.NewStore(pool))
	useModel(cfg.ModelPath, handler, rulesetStore)
	score.RegisterRoutes(mux, handler, cfg.APIKey)
	reviews.RegisterRoutes(mux, reviews.NewStore(pool), cfg.AnalystKeys, cfg.ModelPath != "")
	stats.RegisterRoutes(mux, stats.NewHandler(stats.NewStore(pool)), cfg.APIKey)
	blocklist.RegisterRoutes(mux, blocklist.NewHandler(blocklist.NewStore(pool)), cfg.APIKey, cfg.OperatorKeys)
	manager, evaluation := newSimulationManager(cfg, pool)
	if evaluation != nil {
		defer evaluation.Close()
	}
	simulations.RegisterRoutes(mux, manager, cfg.SimulationEnabled, cfg.APIKey, cfg.OperatorKeys)

	server := platform.NewServer(":"+cfg.Port, mux)
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := shutDownOnSignal(signalCtx, server, manager)
	log.Printf("anti-fraud backend listening on :%s", cfg.Port)
	err = server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		stop()
	}
	<-done
	if !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

// useModel loads the fraud model and its policy and gives them to the score
// handler and the ruleset store. Without a model path the rules decide alone.
// It exits when the model cannot be loaded or used.
func useModel(modelPath string, handler *score.Handler, rulesetStore *rulesets.Store) {
	if modelPath == "" {
		log.Printf("MODEL_PATH is not set, so scoring uses rules only")
		return
	}
	model, err := lgbm.LoadScorer(modelPath)
	if err != nil {
		log.Fatalf("load fraud model: %v", err)
	}
	policy, err := lgbm.LoadPolicy(lgbm.ReportPath(modelPath))
	if err != nil {
		log.Fatalf("load fraud model policy: %v", err)
	}
	if err := handler.UseModel(model, policy); err != nil {
		log.Fatalf("use fraud model %s: %v", model.Version(), err)
	}
	rulesetStore.UseModelThresholds(policy)
	log.Printf("fraud model %s loaded: %d trees, %d features, flag at %v, clear below %v",
		model.Version(), model.Trees(), len(model.Features()), policy.FlagAt, policy.ClearBelow)
}

// newSimulationManager returns the manager of console simulation runs and
// its connection to the evaluation database, or nils when simulations are
// disabled. It exits when the setup fails.
func newSimulationManager(cfg platform.Config, pool *pgxpool.Pool) (*simulations.Manager, *pgxpool.Pool) {
	if !cfg.SimulationEnabled {
		return nil, nil
	}
	setupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	evaluation, err := platform.NewPool(setupCtx, cfg.LabelsDatabaseURL)
	if err != nil {
		log.Fatal("invalid labels database configuration")
	}
	if err := labels.Preflight(setupCtx, evaluation); err != nil {
		log.Fatal(err)
	}
	client := &simulator.Client{BaseURL: "http://127.0.0.1:" + cfg.Port, APIKey: cfg.APIKey, HTTP: &http.Client{Timeout: 10 * time.Second}}
	manager, err := simulations.New(setupCtx, pool, cfg.DatabaseURL, client, evaluation)
	if err != nil {
		log.Fatalf("initialize simulations: %v", err)
	}
	return manager, evaluation
}

// shutDownOnSignal waits for the signal context to end, then drains the
// running simulation and shuts the server down. The returned channel is closed
// when that is done.
func shutDownOnSignal(signalCtx context.Context, server *http.Server, manager *simulations.Manager) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-signalCtx.Done()
		if manager != nil {
			drain, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if err := manager.Close(drain); err != nil {
				log.Printf("drain simulation: %v", err)
			}
			cancel()
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
			_ = server.Close()
		}
	}()
	return done
}
