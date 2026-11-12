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
	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/metrics"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/account"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/accountevent"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/auth"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/chargeback"
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
	stopTracing, err := platform.StartTracing(context.Background(), cfg.Tracing.Endpoint, cfg.Tracing.SampleRatio)
	if err != nil {
		fatal("start tracing", err)
	}
	if cfg.Tracing.Endpoint != "" {
		slog.Info("tracing enabled", "endpoint", cfg.Tracing.Endpoint, "sample_ratio", cfg.Tracing.SampleRatio)
	}

	pool, err := platform.NewPool(context.Background(), cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		fatal("create database pool", err)
	}
	defer pool.Close()
	slog.Info("database pool", "max_conns", pool.Config().MaxConns)

	mux := http.NewServeMux()
	platform.RegisterHealthRoutes(mux, pool)
	authService := auth.NewService(storage.NewAccountStore(pool), storage.NewSessionStore(pool))
	transport.RegisterAuthRoutes(mux, transport.NewAuthHandler(authService))
	access := platform.Access{ServiceKey: cfg.APIKey, Analysts: cfg.AnalystKeys, Operators: cfg.OperatorKeys, Sessions: authService}
	transport.RegisterAccountRoutes(mux, transport.NewAccountHandler(account.NewService(storage.NewAccountStore(pool))), access)
	rulesetStore := storage.NewRulesetStore(pool)
	rulesetService := ruleset.NewService(rulesetStore)
	transport.RegisterRulesetRoutes(mux, transport.NewRulesetHandler(rulesetService), access)
	blocklistStorage, closeBlocklist := newBlocklistStore(cfg, pool)
	defer closeBlocklist()
	reviewStore := storage.NewReviewStore(pool)
	if refresher, ok := blocklistStorage.(storage.BlocklistRefresher); ok {
		reviewStore.UseBlocklistCache(refresher)
	}
	accountEventStore := storage.NewAccountEventStore(pool)
	scoreService := scoresvc.NewService(rulesetStore, blocklistStorage, storage.NewPurchaseStore(pool)).UseAccountEvents(accountEventStore)
	useModel(cfg.ModelPath, scoreService, rulesetService)
	if err := useIPIntel(cfg.IPIntel, scoreService); err != nil {
		fatal("load IP intelligence ranges", err)
	}
	if cfg.MetricsAddr != "" {
		decisionMetrics, err := metrics.New(cfg.MetricsAddr)
		if err != nil {
			fatal("start scoring metrics", err)
		}
		defer decisionMetrics.Close()
		scoreService.UseMetrics(decisionMetrics)
		slog.Info("scoring metrics enabled", "dogstatsd", cfg.MetricsAddr)
	}
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(scoreService).UseLimits(cfg.ScoreLimits), cfg.APIKey)
	transport.RegisterAccountEventRoutes(mux,
		transport.NewAccountEventHandler(accountevent.NewService(accountEventStore)).UseTimeout(cfg.ScoreLimits.Timeout), cfg.APIKey)
	transport.RegisterReviewRoutes(mux, transport.NewReviewHandler(review.NewService(reviewStore), cfg.ModelPath != ""), access)
	transport.RegisterStatsRoutes(mux, transport.NewStatsHandler(stats.NewService(storage.NewStatsStore(pool))), access)
	transport.RegisterBlocklistRoutes(mux, transport.NewBlocklistHandler(blocklist.NewService(blocklistStorage)), access)
	chargebacks := chargeback.NewService(storage.NewChargebackStore(pool))
	transport.RegisterChargebackRoutes(mux, transport.NewChargebackHandler(chargebacks, cfg.ChargebackWebhookSecret))
	evaluation, err := openEvaluationPool(cfg)
	if err != nil {
		fatal("open the evaluation database", err)
	}
	if evaluation != nil {
		defer evaluation.Close()
	}
	quality.RegisterQualityRoutes(mux, quality.NewQualityStore(evaluation), access)
	manager := newSimulationManager(cfg, pool, evaluation)
	transport.RegisterSimulationRoutes(mux, transport.NewSimulationHandler(manager), cfg.SimulationEnabled, access)

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
	flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := stopTracing(flush); err != nil {
		slog.Error("flush traces", "error", err)
	}
	cancel()
	if !errors.Is(err, http.ErrServerClosed) {
		fatal("serve", err)
	}
}

type blocklistStore interface {
	scoresvc.Blocklist
	blocklist.Store
}

func newBlocklistStore(cfg platform.Config, pool *pgxpool.Pool) (blocklistStore, func()) {
	rawStore := storage.NewBlocklistStore(pool)
	if cfg.RedisURL == "" {
		slog.Info("blocklist cache disabled")
		return rawStore, func() {}
	}

	client, err := platform.NewRedis(cfg.RedisURL)
	if err != nil {
		slog.Warn("blocklist cache initialization failed, falling back to database", "error", err)
		return rawStore, func() {}
	}

	cached := storage.NewCachedBlocklistStore(rawStore, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cached.Reload(ctx); err != nil {
		slog.Warn("blocklist cache initial reload failed", "error", err)
	} else {
		entries, err := rawStore.List(ctx)
		count := len(entries)
		if err != nil {
			count = -1
		}
		slog.Info("blocklist cache loaded", "entries", count)
	}
	return cached, func() { _ = client.Close() }
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

// useIPIntel gives the score service the configured IP intelligence provider.
func useIPIntel(cfg platform.IPIntelConfig, service *scoresvc.Service) error {
	mock, err := newIPIntel(cfg)
	if err != nil || mock == nil {
		return err
	}
	service.UseIPIntel(mock)
	return nil
}

// newIPIntel returns the configured mock, or nil when IP intelligence is off.
// A mock ranges file replaces the default ranges.
func newIPIntel(cfg platform.IPIntelConfig) (*ipintel.Mock, error) {
	if cfg.Provider == "" {
		slog.Info("IP_INTEL_PROVIDER is off, so purchases are scored without IP intelligence")
		return nil, nil
	}
	ranges := ipintel.DefaultRanges
	if cfg.MockRangesPath != "" {
		var err error
		if ranges, err = ipintel.LoadRanges(cfg.MockRangesPath); err != nil {
			return nil, err
		}
	}
	slog.Info("IP intelligence enabled", "provider", ipintel.MockName, "ranges", len(ranges))
	return ipintel.NewMock(ranges), nil
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
