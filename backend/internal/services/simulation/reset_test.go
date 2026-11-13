package simulation

import (
	"context"
	"errors"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation/mocks"
	"go.uber.org/mock/gomock"
)

type resetFunc func(context.Context) error

func (f resetFunc) Reset(ctx context.Context) error { return f(ctx) }

func TestResetExcludesActiveController(t *testing.T) {
	store := mocks.NewMockStore(gomock.NewController(t))
	store.EXPECT().Lock(gomock.Any()).Return(nil, false, nil)
	s := &Service{store: store, resetter: resetFunc(func(context.Context) error { t.Fatal("deleted during run"); return nil })}
	var busy *ActiveError
	if err := s.Reset(t.Context(), "op"); !errors.As(err, &busy) {
		t.Fatalf("got %v", err)
	}
}

func TestResetReleasesControllerOnFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mocks.NewMockStore(ctrl)
	controller := mocks.NewMockSimulationController(ctrl)
	store.EXPECT().Lock(gomock.Any()).Return(controller, true, nil)
	controller.EXPECT().Close()
	failure := errors.New("reset rolled back")
	s := &Service{store: store, resetter: resetFunc(func(context.Context) error { return failure })}
	if err := s.Reset(t.Context(), "op"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	s.closed = true
	if err := s.Reset(t.Context(), "op"); !errors.Is(err, ErrShuttingDown) {
		t.Fatal(err)
	}
}
