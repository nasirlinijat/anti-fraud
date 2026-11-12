// Command mock-bank plays the card networks for a scored simulator run: it
// decides which charged purchases cardholders dispute and posts the
// chargebacks that have arrived by the as-of time to the signed chargeback
// webhook. It reads the run's private scenarios through the evaluation role,
// so the API process never needs access to them.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/mockbank"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

type options struct {
	database string
	run      string
	url      string
	secret   string
	seed     uint64
	asOf     time.Time
}

func main() {
	o := parseFlags()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	settled := loadSettledPurchases(ctx, o)
	planned := mockbank.Plan(settled, o.seed)
	arrived := mockbank.ArrivedBy(planned, o.asOf)
	infof("run %s: %d charged purchases, %d chargebacks planned, %d arrived by %s",
		o.run, len(settled), len(planned), len(arrived), o.asOf.Format(time.RFC3339))
	printReasons(arrived)

	sender := &mockbank.Sender{BaseURL: o.url, Secret: o.secret, HTTP: &http.Client{Timeout: 10 * time.Second}}
	sum, err := sender.Send(ctx, arrived)
	infof("recorded %d new chargebacks, %d already recorded; %d still to arrive after %s",
		sum.Recorded, sum.Repeated, len(planned)-len(arrived), o.asOf.Format(time.RFC3339))
	if err != nil {
		fatalf("%v", err)
	}
}

func parseFlags() options {
	o := options{secret: os.Getenv("CHARGEBACK_WEBHOOK_SECRET")}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	asOf := ""
	flag.StringVar(&o.database, "labels-database", os.Getenv("LABELS_DATABASE_URL"), "evaluation database (default $LABELS_DATABASE_URL)")
	flag.StringVar(&o.run, "run", "", "simulator run ID whose purchases the bank charges back")
	flag.StringVar(&o.url, "url", "http://localhost:"+port, "base URL of the backend")
	flag.Uint64Var(&o.seed, "seed", 1, "random seed for which purchases are disputed and when")
	flag.StringVar(&asOf, "as-of", "", "send the chargebacks received by this time, RFC 3339 (default now)")
	flag.Parse()
	switch {
	case o.database == "":
		fatalf("evaluation database is missing: set LABELS_DATABASE_URL or pass -labels-database")
	case o.run == "":
		fatalf("-run is required")
	case o.secret == "":
		fatalf("CHARGEBACK_WEBHOOK_SECRET is not set")
	}
	o.asOf = parseAsOf(asOf)
	return o
}

// parseAsOf returns the as-of time. It exits for a time in the future,
// because the bank cannot have received a dispute yet.
func parseAsOf(value string) time.Time {
	now := time.Now().UTC().Truncate(time.Second)
	if value == "" {
		return now
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		fatalf("as-of must be RFC 3339, for example 2026-11-12T00:00:00Z: %v", err)
	}
	if t.After(now) {
		fatalf("as-of %s is in the future", value)
	}
	return t.UTC()
}

func loadSettledPurchases(ctx context.Context, o options) []labels.SettledPurchase {
	pool, err := pgxpool.New(ctx, o.database)
	if err != nil {
		fatalf("invalid evaluation database configuration")
	}
	defer pool.Close()
	settled, err := labels.SettledPurchases(ctx, pool, o.run)
	if err != nil {
		fatalf("%v", err)
	}
	return settled
}

func printReasons(arrived []models.Chargeback) {
	counts := map[models.ChargebackReason]int{}
	for _, c := range arrived {
		counts[c.Reason]++
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "reason\tcounts as fraud\tarrived\t")
	for _, r := range models.ChargebackReasons {
		fmt.Fprintf(w, "%s\t%t\t%d\t\n", r, r.Fraud(), counts[r])
	}
	_ = w.Flush()
}

func fatalf(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

func infof(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}
