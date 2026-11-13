// Package accountevent records the login and password-reset events that the
// game's account system reports for players. The events feed the account
// takeover rules, the review case, and the account activity model inputs.
package accountevent

import (
	"context"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

//go:generate mockgen -destination=mocks/mock_store.go -package=mocks . Store

// Store saves account events and reports whether an event is new.
type Store interface {
	Save(ctx context.Context, e models.AccountEvent) (bool, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// Record saves a validated account event and reports whether it is new. A
// retry with the same fields returns false. An event that reuses a saved ID
// with different fields returns models.ErrDuplicateAccountEvent.
func (s *Service) Record(ctx context.Context, e models.AccountEvent) (bool, error) {
	return s.store.Save(ctx, e)
}
