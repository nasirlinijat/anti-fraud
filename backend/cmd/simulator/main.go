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

// options are the command-line flags.
type options struct {
	cfg      simulator.Config
	url      string
	apiKey   string
	count    int
	rate     float64
	start    string
	run      string
	database string
	dump     string
}

// live reports whether to send live traffic until interrupted.
func (o options) live() bool { return o.count == 0 }

func main() {
	o := parseFlags()
	if o.apiKey == "" && o.dump == "" {
		log.Fatal("API key is missing: set API_KEY or pass -api-key")
	}
	if o.dump == "" && o.database == "" {
		log.Fatal("LABELS_DATABASE_URL is required for scored runs")
	}
	o.cfg.Start = o.startTime()
	o.cfg.Run = o.runName()

	var purchases []simulator.Purchase
	if !o.live() {
		var err error
		purchases, err = simulator.Generate(o.cfg, o.count)
		if err != nil {
			log.Fatalf("generate purchases: %v", err)
		}
	}
	if o.dump != "" {
		if err := writeDump(o.dump, purchases); err != nil {
			log.Fatalf("write %s: %v", o.dump, err)
		}
		log.Printf("wrote %d purchases to %s (seed %d, run %s)", len(purchases), o.dump, o.cfg.Seed, o.cfg.Run)
		return
	}
	if !o.send(purchases) {
		os.Exit(1)
	}
}

func parseFlags() options {
	o := options{cfg: simulator.DefaultConfig()}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	flag.StringVar(&o.url, "url", "http://localhost:"+port, "base URL of the backend")
	flag.StringVar(&o.apiKey, "api-key", os.Getenv("API_KEY"), "API key for POST /score (default $API_KEY)")
	flag.IntVar(&o.count, "count", 500, "number of purchases to send, 0 for live traffic until Ctrl+C")
	flag.Float64Var(&o.rate, "rate", 20, "purchases sent per second, 0 for as fast as possible")
	flag.StringVar(&o.start, "start", "", "backfill start in RFC 3339 (default: the last purchase happens now)")
	flag.StringVar(&o.run, "run", "", "prefix of all generated IDs (default: seed and start for backfill, unique for live)")
	flag.Uint64Var(&o.cfg.Seed, "seed", o.cfg.Seed, "random seed for the purchase sequence")
	flag.Float64Var(&o.cfg.SuspiciousShare, "suspicious", o.cfg.SuspiciousShare, "share of events that start a special scenario (legitimate or fraud), from 0 to 1")
	flag.IntVar(&o.cfg.Players, "players", o.cfg.Players, "number of regular players")
	flag.DurationVar(&o.cfg.MeanGap, "gap", o.cfg.MeanGap, "mean simulated time between traffic events")
	flag.Float64Var(&o.cfg.MissingFieldShare, "missing", o.cfg.MissingFieldShare, "share of players whose client leaves out each group of optional fields, from 0 to 1")
	flag.StringVar(&o.database, "labels-database", os.Getenv("LABELS_DATABASE_URL"), "evaluation database (required when scoring)")
	flag.StringVar(&o.dump, "dump", "", "write the purchases with their fraud labels to this JSON Lines file instead of sending them")
	flag.Parse()
	return o
}

// startTime checks the count, start and dump flags together and returns the
// time the simulated traffic starts. It exits on an invalid combination.
func (o options) startTime() time.Time {
	switch {
	case o.count < 0:
		log.Fatalf("count must not be negative, got %d", o.count)
	case o.live() && o.dump != "":
		log.Fatal("-dump needs a positive -count")
	case o.live():
		if o.start != "" {
			log.Fatal("-start can only be used with a positive -count")
		}
		return time.Now().UTC()
	case o.start != "":
		t, err := time.Parse(time.RFC3339, o.start)
		if err != nil {
			log.Fatalf("start must be RFC 3339, for example 2026-11-09T10:00:00Z: %v", err)
		}
		return t.UTC()
	}
	// The score API rejects event times more than a few minutes ahead, so
	// the generated traffic ends now instead of starting now.
	start, err := simulator.StartEndingAt(o.cfg, o.count, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		log.Fatalf("generate purchases: %v", err)
	}
	return start
}

// runName returns the prefix of all generated IDs. Call it after the start
// time is set.
func (o options) runName() string {
	switch {
	case o.run != "":
		return o.run
	case o.live():
		return fmt.Sprintf("sim%d-live-%s", o.cfg.Seed, rand.Text())
	default:
		return fmt.Sprintf("sim%d-%s", o.cfg.Seed, o.cfg.Start.Format("20060102T150405"))
	}
}

// send scores the purchases, or live traffic when the count is 0, and prints
// the summary. It reports whether every purchase was scored.
func (o options) send(purchases []simulator.Purchase) bool {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := &simulator.Client{BaseURL: o.url, APIKey: o.apiKey, HTTP: &http.Client{Timeout: 10 * time.Second}}
	pool := o.openLabels(ctx)
	defer pool.Close()
	observed := &labels.Scorer{Pool: pool, Client: client, RunID: o.cfg.Run}
	started := time.Now()
	onError := func(err error) { log.Print(err) }
	var sum simulator.Summary
	var err error
	if o.live() {
		log.Printf("sending live purchases to %s (seed %d, run %s), press Ctrl+C to stop", o.url, o.cfg.Seed, o.cfg.Run)
		sum, err = simulator.RunLive(ctx, observed, o.cfg, o.rate, onError)
	} else {
		log.Printf("sending %d purchases to %s (seed %d, run %s)", len(purchases), o.url, o.cfg.Seed, o.cfg.Run)
		sum, err = simulator.Run(ctx, observed, purchases, o.rate, onError)
	}
	runFailed := err != nil && err != ctx.Err()
	if runFailed {
		log.Printf("stopped early: %v", err)
		log.Printf("evaluation may be pending for run %q; use import-labels -run <run-id> -recover after fixing the error", o.cfg.Run)
	}
	log.Printf("sent %d, failed %d in %s", sum.Sent, sum.Failed, time.Since(started).Round(time.Millisecond))
	printSummary(sum)
	return !runFailed && sum.Failed == 0
}

// openLabels connects to the evaluation database, checks it, and finishes
// any labels a crashed earlier attempt of the same run left pending. It exits
// on failure.
func (o options) openLabels(ctx context.Context) *pgxpool.Pool {
	pool, err := pgxpool.New(ctx, o.database)
	if err != nil {
		log.Fatal("invalid labels database configuration")
	}
	preflight, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = labels.Preflight(preflight, pool)
	cancel()
	if err != nil {
		pool.Close()
		log.Fatal(err)
	}
	if _, err := labels.Recover(ctx, pool, o.cfg.Run); err != nil {
		pool.Close()
		log.Fatal(err)
	}
	return pool
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
