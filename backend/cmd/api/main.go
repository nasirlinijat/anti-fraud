package main

import (
	"context"
	"log"
	"net/http"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
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

	log.Printf("anti-fraud backend listening on :%s", cfg.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, mux))
}
