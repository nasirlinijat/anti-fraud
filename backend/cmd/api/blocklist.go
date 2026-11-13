package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	scoresvc "github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

type blocklistStore interface {
	scoresvc.Blocklist
	blocklist.Store
}

func newBlocklistStore(cfg platform.Config, pool *pgxpool.Pool) (blocklistStore, func()) {
	rawStore := storage.NewBlocklistStore(pool)
	if cfg.RedisURL == "" {
		slog.Info("blocklist cache disabled")
		return rawStore, func() {}
	}

	client, err := platform.NewRedis(cfg.RedisURL)
	if err != nil {
		slog.Warn("blocklist cache initialization failed, falling back to database", "error", err)
		return rawStore, func() {}
	}

	cached := storage.NewCachedBlocklistStore(rawStore, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cached.Reload(ctx); err != nil {
		slog.Warn("blocklist cache initial reload failed", "error", err)
	} else {
		entries, err := rawStore.List(ctx)
		count := len(entries)
		if err != nil {
			count = -1
		}
		slog.Info("blocklist cache loaded", "entries", count)
	}
	return cached, func() { _ = client.Close() }
}
