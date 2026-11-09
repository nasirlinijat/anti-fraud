// Command export-inputs writes the model inputs saved with each decision to a
// CSV file for training. Fraud labels are not exported; training joins them
// from a separate file.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/trainingdata"
)

func main() {
	databaseURL := flag.String("database", os.Getenv("DATABASE_URL"), "PostgreSQL URL (default $DATABASE_URL)")
	out := flag.String("out", "model_inputs.csv", "CSV file to write")
	flag.Parse()
	if *databaseURL == "" {
		log.Fatal("database URL is missing: set DATABASE_URL or pass -database")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *databaseURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	file, err := os.Create(*out)
	if err != nil {
		log.Fatalf("create %s: %v", *out, err)
	}
	sum, err := trainingdata.Export(ctx, pool, features.Names, file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		log.Fatalf("export: %v", err)
	}
	log.Printf("wrote %d decisions to %s; skipped %d with inputs for another feature list", sum.Rows, *out, sum.Skipped)
}
