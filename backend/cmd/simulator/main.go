// Command simulator sends seeded demo purchases to the score API, or writes
// them with their fraud labels to a file for model training.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

func main() {
	cfg := simulator.DefaultConfig()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	url := flag.String("url", "http://localhost:"+port, "base URL of the backend")
	apiKey := flag.String("api-key", os.Getenv("API_KEY"), "API key for POST /score (default $API_KEY)")
	count := flag.Int("count", 500, "number of purchases to send, 0 for live traffic until Ctrl+C")
	rate := flag.Float64("rate", 20, "purchases sent per second, 0 for as fast as possible")
	start := flag.String("start", "", "backfill start in RFC 3339 (default: the last purchase happens now)")
	run := flag.String("run", "", "prefix of all generated IDs (default: seed and start for backfill, unique for live)")
	flag.Uint64Var(&cfg.Seed, "seed", cfg.Seed, "random seed for the purchase sequence")
	flag.Float64Var(&cfg.SuspiciousShare, "suspicious", cfg.SuspiciousShare, "share of events that start a special scenario (legitimate or fraud), from 0 to 1")
	flag.IntVar(&cfg.Players, "players", cfg.Players, "number of regular players")
	flag.DurationVar(&cfg.MeanGap, "gap", cfg.MeanGap, "mean simulated time between traffic events")
	database := flag.String("labels-database", os.Getenv("LABELS_DATABASE_URL"), "evaluation database (required when scoring)")
	dump := flag.String("dump", "", "write the purchases with their fraud labels to this JSON Lines file instead of sending them")
	flag.Parse()

	if *apiKey == "" && *dump == "" {
		log.Fatal("API key is missing: set API_KEY or pass -api-key")
	}
	if *dump == "" && *database == "" {
		log.Fatal("LABELS_DATABASE_URL is required for scored runs")
	}
	live := *count == 0
	switch {
	case *count < 0:
		log.Fatalf("count must not be negative, got %d", *count)
	case live && *dump != "":
		log.Fatal("-dump needs a positive -count")
	case live:
		if *start != "" {
			log.Fatal("-start can only be used with a positive -count")
		}
		cfg.Start = time.Now().UTC()
	case *start != "":
		t, err := time.Parse(time.RFC3339, *start)
		if err != nil {
			log.Fatalf("start must be RFC 3339, for example 2026-11-09T10:00:00Z: %v", err)
		}
		cfg.Start = t.UTC()
	default:
		// The score API rejects event times more than a few minutes ahead, so
		// the generated traffic ends now instead of starting now.
		var err error
		cfg.Start, err = simulator.StartEndingAt(cfg, *count, time.Now().UTC().Truncate(time.Second))
		if err != nil {
			log.Fatalf("generate purchases: %v", err)
		}
	}
	switch {
	case *run != "":
		cfg.Run = *run
	case live:
		cfg.Run = fmt.Sprintf("sim%d-live-%s", cfg.Seed, rand.Text())
	default:
		cfg.Run = fmt.Sprintf("sim%d-%s", cfg.Seed, cfg.Start.Format("20060102T150405"))
	}

	var purchases []simulator.Purchase
	if !live {
		var err error
		purchases, err = simulator.Generate(cfg, *count)
		if err != nil {
			log.Fatalf("generate purchases: %v", err)
		}
	}
	if *dump != "" {
		if err := writeDump(*dump, purchases); err != nil {
			log.Fatalf("write %s: %v", *dump, err)
		}
		log.Printf("wrote %d purchases to %s (seed %d, run %s)", len(purchases), *dump, cfg.Seed, cfg.Run)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := &simulator.Client{BaseURL: *url, APIKey: *apiKey, HTTP: &http.Client{Timeout: 10 * time.Second}}
	pool, err := pgxpool.New(ctx, *database)
	if err != nil {
		log.Fatal("invalid labels database configuration")
	}
	defer pool.Close()
	preflight, cancel := context.WithTimeout(ctx, 5*time.Second)
	if err := labels.Preflight(preflight, pool); err != nil {
		cancel()
		log.Fatal(err)
	}
	cancel()
	if _, err := labels.Recover(ctx, pool, cfg.Run); err != nil {
		log.Fatal(err)
	}
	observed := &labels.Scorer{Pool: pool, Client: client, RunID: cfg.Run}
	started := time.Now()
	onError := func(err error) { log.Print(err) }
	var sum simulator.Summary
	err = nil
	if live {
		log.Printf("sending live purchases to %s (seed %d, run %s), press Ctrl+C to stop", *url, cfg.Seed, cfg.Run)
		sum, err = simulator.RunLive(ctx, observed, cfg, *rate, onError)
	} else {
		log.Printf("sending %d purchases to %s (seed %d, run %s)", len(purchases), *url, cfg.Seed, cfg.Run)
		sum, err = simulator.Run(ctx, observed, purchases, *rate, onError)
	}
	runFailed := err != nil && err != ctx.Err()
	if runFailed {
		log.Printf("stopped early: %v", err)
		log.Printf("evaluation may be pending for run %q; use import-labels -run <run-id> -recover after fixing the error", cfg.Run)
	}
	log.Printf("sent %d, failed %d in %s", sum.Sent, sum.Failed, time.Since(started).Round(time.Millisecond))
	printSummary(sum)
	if runFailed || sum.Failed > 0 {
		os.Exit(1)
	}
}

func writeDump(path string, purchases []simulator.Purchase) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := simulator.WriteJSONL(f, purchases); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func printSummary(sum simulator.Summary) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "pattern\tallow\treview\tblock\t")
	for _, p := range simulator.Patterns {
		o := sum.Outcomes[p]
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t\n", p, o["allow"], o["review"], o["block"])
	}
	w.Flush()
}
