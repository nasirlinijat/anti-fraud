// Command import-labels loads private replay answers or recovers pending labels.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	database := flag.String("database", os.Getenv("LABELS_DATABASE_URL"), "evaluation database (default LABELS_DATABASE_URL)")
	file := flag.String("file", "", "labels CSV file")
	run := flag.String("run", "", "stable evaluation run ID")
	source := flag.String("source", "ieee-cis", "ieee-cis or simulator")
	recover := flag.Bool("recover", false, "recover saved pending labels for this run without sending traffic")
	flag.Parse()
	if *database == "" || *run == "" || (!*recover && *file == "") || (*recover && *file != "") {
		fatalf("set LABELS_DATABASE_URL and -run, and choose -file or -recover")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, *database)
	if err != nil {
		fatalf("invalid evaluation database configuration")
	}
	defer pool.Close()
	if err := labels.Preflight(ctx, pool); err != nil {
		fatalf("%v", err)
	}
	if *recover {
		n, err := labels.Recover(ctx, pool, *run)
		if err != nil {
			fatalf("%v", err)
		}
		fmt.Printf("recovered %d labels\n", n)
		return
	}
	f, err := os.Open(*file)
	if err != nil {
		fatalf("%v", err)
	}
	defer f.Close()
	n, err := labels.ImportCSV(ctx, pool, f, *run, *source)
	if err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("inserted %d labels\n", n)
}

func fatalf(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}
