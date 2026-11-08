package main

import (
	"context"
	"log"
	"net/http"

	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/rulesets"
	"github.com/nasirlinijat/anti-fraud/backend/internal/score"
)

func main() {
	cfg, err := platform.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if cfg.ModelPath == "" {
		log.Printf("MODEL_PATH is not set, so scoring uses rules only")
	} else {
		model, err := lgbm.Load(cfg.ModelPath)
		if err != nil {
			log.Fatalf("load fraud model: %v", err)
		}
		log.Printf("fraud model %s loaded: %d trees, %d features", model.Version(), model.Trees(), len(model.Features()))
	}

	pool, err := platform.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("create database pool: %v", err)
	}
	defer pool.Close()

	mux := http.NewServeMux()
	platform.RegisterHealthRoutes(mux, pool)
	score.RegisterRoutes(mux, score.NewHandler(rulesets.NewStore(pool), score.NewStore(pool)), cfg.APIKey)

	log.Printf("anti-fraud backend listening on :%s", cfg.Port)
	log.Fatal(platform.NewServer(":"+cfg.Port, mux).ListenAndServe())
}
