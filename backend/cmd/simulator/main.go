// Command simulator sends seeded demo purchases to the score API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

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
	count := flag.Int("count", 500, "number of purchases to send")
	rate := flag.Float64("rate", 20, "purchases sent per second, 0 for as fast as possible")
	start := flag.String("start", "", "event time of the first purchase in RFC 3339 (default: the last purchase happens now)")
	run := flag.String("run", "", "prefix of all generated IDs (default built from seed and start)")
	flag.Uint64Var(&cfg.Seed, "seed", cfg.Seed, "random seed; the same seed and start give the same purchases")
	flag.Float64Var(&cfg.SuspiciousShare, "suspicious", cfg.SuspiciousShare, "share of traffic events that are suspicious, from 0 to 1")
	flag.IntVar(&cfg.Players, "players", cfg.Players, "number of regular players")
	flag.DurationVar(&cfg.MeanGap, "gap", cfg.MeanGap, "mean simulated time between traffic events")
	flag.Parse()

	if *apiKey == "" {
		log.Fatal("API key is missing: set API_KEY or pass -api-key")
	}
	if *start != "" {
		t, err := time.Parse(time.RFC3339, *start)
		if err != nil {
			log.Fatalf("start must be RFC 3339, for example 2026-11-09T10:00:00Z: %v", err)
		}
		cfg.Start = t.UTC()
	} else {
		// The score API rejects event times more than a few minutes ahead, so
		// the generated traffic ends now instead of starting now.
		var err error
		cfg.Start, err = simulator.StartEndingAt(cfg, *count, time.Now().UTC().Truncate(time.Second))
		if err != nil {
			log.Fatalf("generate purchases: %v", err)
		}
	}
	cfg.Run = *run
	if cfg.Run == "" {
		cfg.Run = fmt.Sprintf("sim%d-%s", cfg.Seed, cfg.Start.Format("20060102T150405"))
	}

	purchases, err := simulator.Generate(cfg, *count)
	if err != nil {
		log.Fatalf("generate purchases: %v", err)
	}
	log.Printf("sending %d purchases to %s (seed %d, run %s)", len(purchases), *url, cfg.Seed, cfg.Run)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := &simulator.Client{BaseURL: *url, APIKey: *apiKey, HTTP: &http.Client{Timeout: 10 * time.Second}}
	started := time.Now()
	sum, err := simulator.Run(ctx, client, purchases, *rate, func(err error) { log.Print(err) })
	if err != nil {
		log.Printf("stopped early: %v", err)
	}
	log.Printf("sent %d, failed %d in %s", sum.Sent, sum.Failed, time.Since(started).Round(time.Millisecond))
	printSummary(sum)
	if sum.Failed > 0 {
		os.Exit(1)
	}
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
