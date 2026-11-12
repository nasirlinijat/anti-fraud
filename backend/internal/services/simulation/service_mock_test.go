package simulation

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation/mocks"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"go.uber.org/mock/gomock"
)

func liveSettings(requestID string, rate float64) models.SimulationSettings {
	s := DefaultSettings()
	s.RequestID, s.Mode, s.Rate = requestID, "live", rate
	slices.Sort(s.Scenarios)
	return s
}

func newMockService(t *testing.T) (*Service, *mocks.MockStore, *mocks.MockSimulationController) {
	ctrl := gomock.NewController(t)
	store := mocks.NewMockStore(ctrl)
	controller := mocks.NewMockSimulationController(ctrl)
	return &Service{store: store, cancels: map[string]context.CancelFunc{}}, store, controller
}

func TestStartWithoutTheLock(t *testing.T) {
	t.Run("retry of an earlier run", func(t *testing.T) {
		s, store, _ := newMockService(t)
		earlier := models.SimulationRun{ID: "sim-1", Settings: liveSettings("one", 5)}
		store.EXPECT().FindByRequest(gomock.Any(), "op", "one").Return(earlier, true, nil)
		store.EXPECT().Lock(gomock.Any()).Times(0)

		run, created, err := s.Start(context.Background(), "op", liveSettings("one", 5))
		if err != nil || created || run.ID != "sim-1" {
			t.Fatalf("Start = %s, %v, %v; want the earlier run", run.ID, created, err)
		}
	})

	t.Run("request id reused with other settings", func(t *testing.T) {
		s, store, _ := newMockService(t)
		store.EXPECT().FindByRequest(gomock.Any(), "op", "one").Return(models.SimulationRun{ID: "sim-1", Settings: liveSettings("one", 5)}, true, nil)

		if _, _, err := s.Start(context.Background(), "op", liveSettings("one", 6)); !errors.Is(err, ErrRequestReused) {
			t.Fatalf("err = %v, want ErrRequestReused", err)
		}
	})

	t.Run("another run holds the lock", func(t *testing.T) {
		s, store, _ := newMockService(t)
		store.EXPECT().FindByRequest(gomock.Any(), "op", "two").Return(models.SimulationRun{}, false, nil)
		store.EXPECT().Lock(gomock.Any()).Return(nil, false, nil)
		store.EXPECT().Active(gomock.Any()).Return(models.SimulationRun{ID: "sim-1"}, nil)

		_, _, err := s.Start(context.Background(), "op", liveSettings("two", 5))
		var active *ActiveError
		if !errors.As(err, &active) || active.RunID != "sim-1" {
			t.Fatalf("err = %v, want the active run sim-1", err)
		}
	})

	t.Run("shutting down", func(t *testing.T) {
		s, _, _ := newMockService(t)
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Start(context.Background(), "op", liveSettings("three", 5)); !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("err = %v, want ErrShuttingDown", err)
		}
	})

	t.Run("an earlier run committed by another process", func(t *testing.T) {
		s, store, controller := newMockService(t)
		store.EXPECT().FindByRequest(gomock.Any(), "op", "four").Return(models.SimulationRun{}, false, nil)
		store.EXPECT().Lock(gomock.Any()).Return(controller, true, nil)
		gomock.InOrder(
			controller.EXPECT().Recover(gomock.Any()).Return(nil),
			controller.EXPECT().FindByRequest(gomock.Any(), "op", "four").Return(models.SimulationRun{ID: "sim-4", Settings: liveSettings("four", 5)}, true, nil),
			controller.EXPECT().Close(),
		)
		controller.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

		run, created, err := s.Start(context.Background(), "op", liveSettings("four", 5))
		if err != nil || created || run.ID != "sim-4" {
			t.Fatalf("Start = %s, %v, %v; want sim-4", run.ID, created, err)
		}
	})
}

func TestTrackedScorer(t *testing.T) {
	req := simulator.Request{TransactionID: "txn-1"}

	t.Run("checkpoints the outcome", func(t *testing.T) {
		controller := mocks.NewMockSimulationController(gomock.NewController(t))
		gomock.InOrder(
			controller.EXPECT().MarkPending(gomock.Any(), "sim-1", "txn-1").Return(true, nil),
			controller.EXPECT().Checkpoint(gomock.Any(), "sim-1", "txn-1", "review").Return(nil),
		)
		tracker := &trackedScorer{client: scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
			return simulator.Result{Outcome: "review"}, nil
		}), c: controller, id: "sim-1", cancel: func() {}}

		got, err := tracker.Score(context.Background(), req)
		if err != nil || got.Outcome != "review" || tracker.fatal != nil {
			t.Fatalf("Score = %+v, %v, fatal %v", got, err, tracker.fatal)
		}
	})

	t.Run("a send error is reconciled by the saved outcome", func(t *testing.T) {
		controller := mocks.NewMockSimulationController(gomock.NewController(t))
		gomock.InOrder(
			controller.EXPECT().MarkPending(gomock.Any(), "sim-1", "txn-1").Return(true, nil),
			controller.EXPECT().RejectLabel(gomock.Any(), "txn-1", gomock.Any()).Return(false),
			controller.EXPECT().OutcomeOf(gomock.Any(), "txn-1").Return("block", nil),
			controller.EXPECT().Checkpoint(gomock.Any(), "sim-1", "txn-1", "block").Return(nil),
		)
		tracker := &trackedScorer{client: scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
			return simulator.Result{}, errors.New("connection reset")
		}), c: controller, id: "sim-1", cancel: func() {}}

		got, err := tracker.Score(context.Background(), req)
		if err != nil || got.Outcome != "block" {
			t.Fatalf("Score = %+v, %v; want the saved block", got, err)
		}
	})

	t.Run("a refused request rejects its label and stops the run", func(t *testing.T) {
		controller := mocks.NewMockSimulationController(gomock.NewController(t))
		refused := &simulator.HTTPError{Status: 400}
		gomock.InOrder(
			controller.EXPECT().MarkPending(gomock.Any(), "sim-1", "txn-1").Return(true, nil),
			controller.EXPECT().RejectLabel(gomock.Any(), "txn-1", refused).Return(true),
		)
		controller.EXPECT().OutcomeOf(gomock.Any(), gomock.Any()).Times(0)
		controller.EXPECT().Checkpoint(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		cancelled := false
		tracker := &trackedScorer{client: scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
			return simulator.Result{}, refused
		}), c: controller, id: "sim-1", cancel: func() { cancelled = true }}

		_, err := tracker.Score(context.Background(), req)
		var fatal simulator.FatalError
		if !errors.As(err, &fatal) || tracker.fatal == nil || !cancelled {
			t.Fatalf("err = %v, fatal %v, cancelled %v; want a FatalError that cancels the run", err, tracker.fatal, cancelled)
		}
	})

	t.Run("a stopped run sends nothing", func(t *testing.T) {
		controller := mocks.NewMockSimulationController(gomock.NewController(t))
		controller.EXPECT().MarkPending(gomock.Any(), "sim-1", "txn-1").Return(false, nil)
		cancelled := false
		tracker := &trackedScorer{client: scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
			t.Fatal("a stopped run sent a purchase")
			return simulator.Result{}, nil
		}), c: controller, id: "sim-1", cancel: func() { cancelled = true }}

		if _, err := tracker.Score(context.Background(), req); !errors.Is(err, context.Canceled) || !cancelled {
			t.Fatalf("err = %v, cancelled %v; want a cancelled run", err, cancelled)
		}
	})

	t.Run("an invalid outcome fails the run", func(t *testing.T) {
		controller := mocks.NewMockSimulationController(gomock.NewController(t))
		controller.EXPECT().MarkPending(gomock.Any(), "sim-1", "txn-1").Return(true, nil)
		controller.EXPECT().Checkpoint(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		tracker := &trackedScorer{client: scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
			return simulator.Result{Outcome: "maybe"}, nil
		}), c: controller, id: "sim-1", cancel: func() {}}

		if _, err := tracker.Score(context.Background(), req); err == nil || tracker.fatal == nil {
			t.Fatal("an invalid outcome was accepted")
		}
	})

	t.Run("a label that cannot be saved stops the run", func(t *testing.T) {
		controller := mocks.NewMockSimulationController(gomock.NewController(t))
		controller.EXPECT().PrepareLabel(gomock.Any(), "sim-1", gomock.Any()).Return(errors.New("labels database down"))
		controller.EXPECT().MarkPending(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		tracker := &trackedScorer{c: controller, id: "sim-1", cancel: func() {}}

		_, err := tracker.ScorePurchase(context.Background(), simulator.Purchase{Request: req})
		var fatal simulator.FatalError
		if !errors.As(err, &fatal) {
			t.Fatalf("err = %v, want a FatalError", err)
		}
	})
}
