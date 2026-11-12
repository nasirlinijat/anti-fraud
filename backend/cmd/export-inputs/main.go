// Command export-inputs writes the model inputs saved with each decision to a
// CSV file for training. With -chargeback-labels it also writes the labels a
// business really has, taken from recorded chargebacks. The private
// evaluation labels of simulator and replay runs are never exported here.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/trainingdata"
)

type options struct {
	databaseURL      string
	out              string
	chargebackLabels string
	labels           trainingdata.LabelOptions
}

func main() {
	o := parseFlags()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, o.databaseURL)
	if err != nil {
		fatalf("connect: %v", err)
	}
	defer pool.Close()

	exportInputs(ctx, pool, o.out)
	if o.chargebackLabels != "" {
		exportChargebackLabels(ctx, pool, o)
	}
}

func parseFlags() options {
	var o options
	var labels labelFlags
	flag.StringVar(&o.databaseURL, "database", os.Getenv("DATABASE_URL"), "PostgreSQL URL (default $DATABASE_URL)")
	flag.StringVar(&o.out, "out", "model_inputs.csv", "CSV file to write")
	flag.StringVar(&o.chargebackLabels, "chargeback-labels", "", "also write transaction_id,is_fraud labels from chargebacks to this CSV file")
	flag.IntVar(&labels.maturityDays, "maturity-days", trainingdata.DefaultMaturityDays, "with -chargeback-labels, label only purchases at least this many days older than -as-of; 0 labels every purchase up to -as-of")
	flag.StringVar(&labels.asOf, "as-of", "", "with -chargeback-labels, the time the labels are taken at in RFC 3339, not in the future (default now)")
	flag.StringVar(&labels.since, "since", "", "with -chargeback-labels, label only purchases with an event time at or after this RFC 3339 time")
	flag.StringVar(&labels.until, "until", "", "with -chargeback-labels, label only purchases with an event time before this RFC 3339 time")
	flag.Parse()
	if o.databaseURL == "" {
		fatalf("database URL is missing: set DATABASE_URL or pass -database")
	}
	var err error
	if o.labels, err = labels.options(time.Now().UTC()); err != nil {
		fatalf("%v", err)
	}
	return o
}

// labelFlags are the chargeback label flags as given on the command line.
type labelFlags struct {
	asOf, since, until string
	maturityDays       int
}

// options checks the label flags against the current time and returns the
// label options. The as-of time cannot be in the future, because chargebacks
// that the bank has not received yet cannot be known.
func (f labelFlags) options(now time.Time) (trainingdata.LabelOptions, error) {
	opts := trainingdata.LabelOptions{AsOf: now, MaturityDays: f.maturityDays}
	if f.maturityDays < 0 {
		return opts, fmt.Errorf("maturity-days must not be negative, got %d", f.maturityDays)
	}
	var err error
	if f.asOf != "" {
		if opts.AsOf, err = parseTime("as-of", f.asOf); err != nil {
			return opts, err
		}
	}
	if opts.AsOf.After(now) {
		return opts, fmt.Errorf("as-of %s is in the future", f.asOf)
	}
	if opts.Since, err = parseTime("since", f.since); err != nil {
		return opts, err
	}
	if opts.Until, err = parseTime("until", f.until); err != nil {
		return opts, err
	}
	if !opts.Since.IsZero() && !opts.Until.IsZero() && !opts.Until.After(opts.Since) {
		return opts, fmt.Errorf("until must be after since")
	}
	return opts, nil
}

// parseTime parses an optional RFC 3339 flag value into UTC. An empty value
// gives the zero time.
func parseTime(name, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC 3339, for example 2026-11-12T00:00:00Z: %w", name, err)
	}
	return t.UTC(), nil
}

func exportInputs(ctx context.Context, pool *pgxpool.Pool, path string) {
	file, err := os.Create(filepath.Clean(path))
	if err != nil {
		fatalf("create %s: %v", path, err)
	}
	sum, err := trainingdata.Export(ctx, pool, features.Names, file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		fatalf("export: %v", err)
	}
	infof("wrote %d decisions to %s; skipped %d with inputs for another feature list", sum.Rows, path, sum.Skipped)
}

func exportChargebackLabels(ctx context.Context, pool *pgxpool.Pool, o options) {
	file, err := os.Create(filepath.Clean(o.chargebackLabels))
	if err != nil {
		fatalf("create %s: %v", o.chargebackLabels, err)
	}
	sum, err := trainingdata.ExportChargebackLabels(ctx, pool, o.labels, file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		fatalf("export chargeback labels: %v", err)
	}
	infof("wrote %d fraud and %d legitimate labels as of %s to %s; left out %d purchases inside the %d-day maturity window",
		sum.Fraud, sum.Legitimate, o.labels.AsOf.Format(time.RFC3339), o.chargebackLabels, sum.Immature, o.labels.MaturityDays)
}

func fatalf(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

func infof(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}
