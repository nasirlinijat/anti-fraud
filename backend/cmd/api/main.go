package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	scoresvc "github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	quality "github.com/nasirlinijat/anti-fraud/backend/internal/stats"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
)

func main() {
	logger := platform.NewLogger(os.Stdout)
	slog.SetDefault(logger)
	cfg, err := platform.LoadConfig()
	if err != nil {
		fatal("load config", err)
	}

	pool, err := platform.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fatal("create database pool", err)
	}
	defer pool.Close()

	mux := http.NewServeMux()
	platform.RegisterHealthRoutes(mux, pool)
	rulesetStore := storage.NewRulesetStore(pool)
	rulesetService := ruleset.NewService(rulesetStore)
	transport.RegisterRulesetRoutes(mux, transport.NewRulesetHandler(rulesetService), cfg.APIKey, cfg.OperatorKeys)
	scoreService := scoresvc.NewService(rulesetStore, storage.NewBlocklistStore(pool), storage.NewPurchaseStore(pool))
	useModel(cfg.ModelPath, scoreService, rulesetService)
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(scoreService), cfg.APIKey)
	transport.RegisterReviewRoutes(mux, transport.NewReviewHandler(review.NewService(storage.NewReviewStore(pool)), cfg.ModelPath != ""), cfg.AnalystKeys)
	transport.RegisterStatsRoutes(mux, transport.NewStatsHandler(stats.NewService(storage.NewStatsStore(pool))), cfg.APIKey)
	transport.RegisterBlocklistRoutes(mux, transport.NewBlocklistHandler(blocklist.NewService(storage.NewBlocklistStore(pool))), cfg.APIKey, cfg.OperatorKeys)
	evaluation, err := openEvaluationPool(cfg)
	if err != nil {
		fatal("open the evaluation database", err)
	}
	if evaluation != nil {
		defer evaluation.Close()
	}
	quality.RegisterQualityRoutes(mux, quality.NewQualityStore(evaluation), cfg.APIKey)
	manager := newSimulationManager(cfg, pool, evaluation)
	transport.RegisterSimulationRoutes(mux, transport.NewSimulationHandler(manager), cfg.SimulationEnabled, cfg.APIKey, cfg.OperatorKeys)

	server := platform.NewServer(":"+cfg.Port, transport.Middleware(mux))
	server.ErrorLog = slog.NewLogLogger(logger.Handler(), slog.LevelWarn)
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := shutDownOnSignal(signalCtx, server, manager)
	slog.Info("anti-fraud backend listening", "port", cfg.Port)
	err = server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		stop()
	}
	<-done
	if !errors.Is(err, http.ErrServerClosed) {
		fatal("serve", err)
	}
}

// useModel loads the fraud model and its policy and gives them to the score
// service and the ruleset service. Without a model path the rules decide alone.
// It exits when the model cannot be loaded or used.
func useModel(modelPath string, service *scoresvc.Service, rulesets *ruleset.Service) {
	if modelPath == "" {
		slog.Info("MODEL_PATH is not set, so scoring uses rules only")
		return
	}
	model, err := lgbm.LoadScorer(modelPath)
	if err != nil {
		fatal("load fraud model", err)
	}
	policy, err := lgbm.LoadPolicy(lgbm.ReportPath(modelPath))
	if err != nil {
		fatal("load fraud model policy", err)
	}
	if err := service.UseModel(model, policy); err != nil {
		fatal("use fraud model "+model.Version(), err)
	}
	rulesets.UseModelThresholds(policy)
	slog.Info("fraud model loaded",
		"model_version", model.Version(), "trees", model.Trees(), "features", len(model.Features()),
		"flag_at", policy.FlagAt, "clear_below", policy.ClearBelow)
}

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
	evaluation, err := platform.NewPool(setupCtx, cfg.LabelsDatabaseURL)
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

// shutDownOnSignal waits for the signal context to end, then drains the
// running simulation and shuts the server down. The returned channel is closed
// when that is done.
func shutDownOnSignal(signalCtx context.Context, server *http.Server, manager *simulation.Service) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-signalCtx.Done()
		if manager != nil {
			drain, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if err := manager.Close(drain); err != nil {
				slog.Error("drain simulation", "error", err)
			}
			cancel()
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			slog.Error("shutdown", "error", err)
			_ = server.Close()
		}
	}()
	return done
}

func fatal(msg string, err error) {
	if err != nil {
		slog.Error(msg, "error", err)
	} else {
		slog.Error(msg)
	}
	os.Exit(1)
}
