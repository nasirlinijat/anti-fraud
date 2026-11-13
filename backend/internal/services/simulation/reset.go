package simulation

import (
	"context"
	"errors"
	"log/slog"
)

// Resetter deletes demonstration state atomically.
type Resetter interface{ Reset(context.Context) error }

// ResetCache forces database fallback while reset changes the blocklist.
type ResetCache interface {
	DuringReset(context.Context, func() error) error
}

// UseReset configures reset before the service starts accepting requests.
func (s *Service) UseReset(store Resetter, cache ResetCache) *Service {
	s.resetter, s.resetCache = store, cache
	return s
}

// Reset requires an idle controller. External ingestion must already be paused.
func (s *Service) Reset(ctx context.Context, actorID string) error {
	if !s.mu.TryLock() {
		return &ActiveError{}
	}
	defer s.mu.Unlock()
	if s.closed {
		return ErrShuttingDown
	}
	if s.resetter == nil {
		return errors.New("reset is unavailable")
	}
	controller, ok, err := s.store.Lock(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return &ActiveError{}
	}
	defer controller.Close()
	reset := func() error { return s.resetter.Reset(ctx) }
	if s.resetCache != nil {
		err = s.resetCache.DuringReset(ctx, reset)
	} else {
		err = reset()
	}
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "demo data reset", "actor", actorID)
	return nil
}
