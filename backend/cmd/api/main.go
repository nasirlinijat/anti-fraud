package main

import (
	"context"
	"log"
	"net/http"

	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/reviews"
	"github.com/nasirlinijat/anti-fraud/backend/internal/rulesets"
	"github.com/nasirlinijat/anti-fraud/backend/internal/score"
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
	if cfg.ModelPath == "" {
		log.Printf("MODEL_PATH is not set, so scoring uses rules only")
	} else {
		model, err := lgbm.LoadScorer(cfg.ModelPath)
		if err != nil {
			log.Fatalf("load fraud model: %v", err)
		}
		policy, err := lgbm.LoadPolicy(lgbm.ReportPath(cfg.ModelPath))
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
	score.RegisterRoutes(mux, handler, cfg.APIKey)
	reviews.RegisterRoutes(mux, reviews.NewStore(pool), cfg.AnalystKeys, cfg.ModelPath != "")
	stats.RegisterRoutes(mux, stats.NewHandler(stats.NewStore(pool)), cfg.APIKey)
	blocklist.RegisterRoutes(mux, blocklist.NewHandler(blocklist.NewStore(pool)), cfg.APIKey, cfg.OperatorKeys)

	log.Printf("anti-fraud backend listening on :%s", cfg.Port)
	log.Fatal(platform.NewServer(":"+cfg.Port, mux).ListenAndServe())
}
