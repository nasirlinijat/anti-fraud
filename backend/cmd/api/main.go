package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
)

func main() {
	logger := platform.NewLogger(os.Stdout)
	slog.SetDefault(logger)
	cfg, err := platform.LoadConfig()
	if err != nil {
		fatal("load config", err)
	}
	stopTracing, err := platform.StartTracing(context.Background(), cfg.Tracing.Endpoint, cfg.Tracing.SampleRatio)
	if err != nil {
		fatal("start tracing", err)
	}
	if cfg.Tracing.Endpoint != "" {
		slog.Info("tracing enabled", "endpoint", cfg.Tracing.Endpoint, "sample_ratio", cfg.Tracing.SampleRatio)
	}

	pool, err := platform.NewPool(context.Background(), cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		fatal("create database pool", err)
	}
	defer pool.Close()
	slog.Info("database pool", "max_conns", pool.Config().MaxConns)

	mux := http.NewServeMux()
	manager, closeRoutes := registerRoutes(mux, cfg, pool)
	defer closeRoutes()

	server := platform.NewServer(":"+cfg.Port, transport.Middleware(mux))
	server.ErrorLog = slog.NewLogLogger(logger.Handler(), slog.LevelWarn)
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := shutDownOnSignal(signalCtx, server, manager)
	slog.Info("anti-fraud backend listening", "port", cfg.Port)
	err = server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		stop()
	}
	<-done
	flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := stopTracing(flush); err != nil {
		slog.Error("flush traces", "error", err)
	}
	cancel()
	if !errors.Is(err, http.ErrServerClosed) {
		fatal("serve", err)
	}
}

func fatal(msg string, err error) {
	if err != nil {
		slog.Error(msg, "error", err)
	} else {
		slog.Error(msg)
	}
	os.Exit(1)
}
