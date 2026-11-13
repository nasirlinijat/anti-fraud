package main

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
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
	quality "github.com/nasirlinijat/anti-fraud/backend/internal/stats"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
)

func registerRoutes(mux *http.ServeMux, cfg platform.Config, pool *pgxpool.Pool) (*simulation.Service, func()) {
	var closers []func()
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	platform.RegisterHealthRoutes(mux, pool)
	authService := auth.NewService(storage.NewAccountStore(pool), storage.NewSessionStore(pool))
	transport.RegisterAuthRoutes(mux, transport.NewAuthHandler(authService))
	access := platform.Access{ServiceKey: cfg.APIKey, Analysts: cfg.AnalystKeys, Operators: cfg.OperatorKeys, Sessions: authService}
	transport.RegisterAccountRoutes(mux, transport.NewAccountHandler(account.NewService(storage.NewAccountStore(pool))), access)

	rulesetStore := storage.NewRulesetStore(pool)
	rulesetService := ruleset.NewService(rulesetStore)
	transport.RegisterRulesetRoutes(mux, transport.NewRulesetHandler(rulesetService), access)

	blocklistStorage, closeBlocklist := newBlocklistStore(cfg, pool)
	closers = append(closers, closeBlocklist)
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
	if closeMetrics := useMetrics(cfg.MetricsAddr, scoreService); closeMetrics != nil {
		closers = append(closers, closeMetrics)
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
		closers = append(closers, evaluation.Close)
	}
	quality.RegisterQualityRoutes(mux, quality.NewQualityStore(evaluation), access)
	cache, _ := blocklistStorage.(simulation.ResetCache)
	manager := newSimulationManager(cfg, pool, evaluation, cache)
	transport.RegisterSimulationRoutes(mux, transport.NewSimulationHandler(manager), cfg.SimulationEnabled, access)

	return manager, closeAll
}

func useMetrics(addr string, service *scoresvc.Service) func() {
	if addr == "" {
		return nil
	}
	decisionMetrics, err := metrics.New(addr)
	if err != nil {
		fatal("start scoring metrics", err)
	}
	service.UseMetrics(decisionMetrics)
	slog.Info("scoring metrics enabled", "dogstatsd", addr)
	return func() { _ = decisionMetrics.Close() }
}
