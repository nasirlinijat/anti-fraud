package storage

import (
	"context"
	"log/slog"
)

// DuringReset serializes with refreshes and reloads. Failed cache repair keeps
// this process on PostgreSQL, even if Redis later returns with stale keys.
func (s *CachedBlocklistStore) DuringReset(ctx context.Context, reset func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bypass.Store(true)
	err := reset()
	if cacheErr := s.reloadLocked(ctx); cacheErr != nil {
		slog.WarnContext(ctx, "blocklist cache repair after reset", "error", cacheErr)
	}
	return err
}
