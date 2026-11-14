// Command seed-demo resets and populates a local demonstration stack.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/demoreset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

const (
	runID     = "demo-42"
	openCount = 10
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := seed(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func seed(ctx context.Context) error {
	local, err := loadDemo()
	if err != nil {
		return err
	}
	purchases, err := demoPurchases(ctx)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, os.Getenv("LABELS_DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("invalid LABELS_DATABASE_URL")
	}
	defer pool.Close()
	if err := local.preflight(ctx, pool); err != nil {
		return err
	}
	if err := demoreset.Reset(ctx, local.reset); err != nil {
		return fmt.Errorf("reset before seeding: %w", err)
	}

	fmt.Printf("Seeding %d purchases across seven days (seed 42, run %s)\n", len(purchases), runID)
	scorer := &labels.Scorer{Pool: pool, RunID: runID, Client: &simulator.Client{
		BaseURL: local.reset.BaseURL, APIKey: local.serviceKey, HTTP: local.http,
	}}
	if err := sendPurchases(ctx, scorer, purchases); err != nil {
		return fmt.Errorf("backfill stopped: %w", err)
	}
	reviews, err := labels.DemoReviews(ctx, pool, runID)
	if err != nil {
		return err
	}
	resolved, err := local.resolveReviews(ctx, reviews)
	if err != nil {
		return err
	}
	fmt.Printf("Seeded %d purchases, resolved %d reviews, left %d open\n", len(purchases), resolved, len(reviews)-resolved)
	return nil
}

func demoPurchases(ctx context.Context) ([]simulator.Purchase, error) {
	cfg := simulator.DefaultConfig()
	cfg.Seed, cfg.Run = 42, runID
	cfg.Start = time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	cfg.MeanGap = 10 * time.Minute
	cfg.SuspiciousShare = 0.25
	return simulator.GenerateUntil(ctx, cfg, cfg.Start.AddDate(0, 0, 7))
}

func sendPurchases(ctx context.Context, scorer *labels.Scorer, purchases []simulator.Purchase) error {
	// Leave room below the default API limit of 100 scores per second.
	ticker := time.NewTicker(time.Second / 75)
	defer ticker.Stop()
	for _, purchase := range purchases {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		for _, event := range purchase.AccountEvents {
			if err := scorer.SendAccountEvent(ctx, event); err != nil {
				return err
			}
		}
		if _, err := scorer.ScorePurchase(ctx, purchase); err != nil {
			return err
		}
	}
	return nil
}

func demoURL() (string, error) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("PORT must be between 1 and 65535")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port), nil
}

type demo struct {
	reset      demoreset.Config
	serviceKey string
	http       *http.Client
}

func loadDemo() (demo, error) {
	baseURL, err := demoURL()
	if err != nil {
		return demo{}, err
	}
	local := demo{
		reset: demoreset.Config{
			BaseURL: baseURL, SessionToken: os.Getenv("RESET_SESSION_TOKEN"),
		},
		serviceKey: os.Getenv("API_KEY"),
		http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
	if local.serviceKey == "" || local.reset.SessionToken == "" || os.Getenv("LABELS_DATABASE_URL") == "" {
		return demo{}, fmt.Errorf("set API_KEY, RESET_SESSION_TOKEN, and LABELS_DATABASE_URL")
	}
	return local, nil
}
