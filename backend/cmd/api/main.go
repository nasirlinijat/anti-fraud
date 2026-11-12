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
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/auth"
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
	authService := auth.NewService(storage.NewAccountStore(pool), storage.NewSessionStore(pool))
	transport.RegisterAuthRoutes(mux, transport.NewAuthHandler(authService))
	access := platform.Access{ServiceKey: cfg.APIKey, Analysts: cfg.AnalystKeys, Operators: cfg.OperatorKeys, Sessions: authService}
	rulesetStore := storage.NewRulesetStore(pool)
	rulesetService := ruleset.NewService(rulesetStore)
	transport.RegisterRulesetRoutes(mux, transport.NewRulesetHandler(rulesetService), access)
	scoreService := scoresvc.NewService(rulesetStore, storage.NewBlocklistStore(pool), storage.NewPurchaseStore(pool))
	useModel(cfg.ModelPath, scoreService, rulesetService)
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(scoreService), cfg.APIKey)
	transport.RegisterReviewRoutes(mux, transport.NewReviewHandler(review.NewService(storage.NewReviewStore(pool)), cfg.ModelPath != ""), access)
	transport.RegisterStatsRoutes(mux, transport.NewStatsHandler(stats.NewService(storage.NewStatsStore(pool))), access)
	transport.RegisterBlocklistRoutes(mux, transport.NewBlocklistHandler(blocklist.NewService(storage.NewBlocklistStore(pool))), access)
	evaluation, err := openEvaluationPool(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if evaluation != nil {
		defer evaluation.Close()
	}
	quality.RegisterQualityRoutes(mux, quality.NewQualityStore(evaluation), access)
	manager := newSimulationManager(cfg, pool, evaluation)
	transport.RegisterSimulationRoutes(mux, transport.NewSimulationHandler(manager), cfg.SimulationEnabled, access)

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
// service and the ruleset service. Without a model path the rules decide alone.
// It exits when the model cannot be loaded or used.
func useModel(modelPath string, service *scoresvc.Service, rulesets *ruleset.Service) {
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
	if err := service.UseModel(model, policy); err != nil {
		log.Fatalf("use fraud model %s: %v", model.Version(), err)
	}
	rulesets.UseModelThresholds(policy)
	log.Printf("fraud model %s loaded: %d trees, %d features, flag at %v, clear below %v",
		model.Version(), model.Trees(), len(model.Features()), policy.FlagAt, policy.ClearBelow)
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
		log.Print("quality reporting is unavailable: invalid evaluation configuration")
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
		log.Fatalf("initialize simulations: %v", err)
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
