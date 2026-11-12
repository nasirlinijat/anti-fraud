package simulation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

//go:generate mockgen -destination=mocks/mock_store.go -package=mocks . Store
//go:generate mockgen -destination=mocks/mock_controller.go -package=mocks github.com/nasirlinijat/anti-fraud/backend/internal/models SimulationController

const defaultBackfillCount = 500

var (
	ErrShuttingDown  = errors.New("simulation service is shutting down")
	ErrRequestReused = errors.New("request_id was used with different settings")
)

// ValidationError lists the problems found in the settings, keyed by field name.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return "invalid simulation settings"
}

// ActiveError means another run holds the controller lock. RunID is empty
// when that run could not be read.
type ActiveError struct {
	RunID string
}

func (e *ActiveError) Error() string {
	return "a simulation is already active"
}

type Store interface {
	Lock(ctx context.Context) (models.SimulationController, bool, error)
	Get(ctx context.Context, id string) (models.SimulationRun, error)
	FindByRequest(ctx context.Context, operatorID, requestID string) (models.SimulationRun, bool, error)
	Active(ctx context.Context) (models.SimulationRun, error)
	List(ctx context.Context, before string, limit int) ([]models.SimulationRun, error)
	RequestStop(ctx context.Context, id, operatorID string) (bool, error)
}

// Service owns background workers; database locks also exclude other processes.
type Service struct {
	store   Store
	client  simulator.Scorer
	mu      sync.Mutex
	closed  bool
	cancels map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// NewService returns the service. When no other controller is active it
// recovers the runs a previous process left behind.
func NewService(ctx context.Context, store Store, client simulator.Scorer) (*Service, error) {
	s := &Service{store: store, client: client, cancels: map[string]context.CancelFunc{}}
	c, ok, err := store.Lock(ctx)
	if err != nil {
		return nil, err
	}
	if ok {
		defer c.Close()
		err = c.Recover(ctx)
	}
	return s, err
}

// DefaultSettings returns the settings a start request begins with before
// its own fields are applied.
func DefaultSettings() models.SimulationSettings {
	return models.SimulationSettings{Seed: 1, Rate: 5, Scenarios: slices.Clone(simulator.Patterns)}
}

// Close rejects starts and drains in-flight work within the caller's budget.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Start begins a run in the background. A request ID used before returns
// that run with created false, or ErrRequestReused for different settings.
// It returns *ValidationError for invalid settings and *ActiveError while
// another run is active.
func (s *Service) Start(ctx context.Context, operatorID string, settings models.SimulationSettings) (models.SimulationRun, bool, error) {
	if settings.Mode == "backfill" && settings.Count == 0 {
		settings.Count = defaultBackfillCount
	}
	if fields := validate(&settings); len(fields) > 0 {
		return models.SimulationRun{}, false, &ValidationError{Fields: fields}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return models.SimulationRun{}, false, ErrShuttingDown
	}
	if run, found, err := s.store.FindByRequest(ctx, operatorID, settings.RequestID); found || err != nil {
		return sameSettings(run, settings, err)
	}
	c, ok, err := s.store.Lock(ctx)
	if err != nil {
		return models.SimulationRun{}, false, err
	}
	if !ok {
		active, err := s.store.Active(ctx)
		if err != nil {
			return models.SimulationRun{}, false, &ActiveError{}
		}
		return models.SimulationRun{}, false, &ActiveError{RunID: active.ID}
	}
	keep := false
	defer func() {
		if !keep {
			c.Close()
		}
	}()
	if err := c.Recover(ctx); err != nil {
		return models.SimulationRun{}, false, err
	}
	// Another process may have committed this idempotency key before releasing ownership.
	if run, found, err := c.FindByRequest(ctx, operatorID, settings.RequestID); found || err != nil {
		return sameSettings(run, settings, err)
	}
	run, err := c.Create(ctx, "sim-"+rand.Text(), operatorID, settings)
	if err != nil {
		return models.SimulationRun{}, false, err
	}
	workCtx, cancel := context.WithCancel(context.Background())
	s.cancels[run.ID] = cancel
	s.wg.Add(1)
	keep = true
	go s.work(workCtx, c, run)
	return run, true, nil
}

// sameSettings returns an earlier run of the request ID when its settings
// match, and ErrRequestReused when they do not.
func sameSettings(run models.SimulationRun, settings models.SimulationSettings, err error) (models.SimulationRun, bool, error) {
	if err != nil {
		return models.SimulationRun{}, false, err
	}
	if !reflect.DeepEqual(run.Settings, settings) {
		return models.SimulationRun{}, false, ErrRequestReused
	}
	return run, false, nil
}

func (s *Service) Get(ctx context.Context, id string) (models.SimulationRun, error) {
	return s.store.Get(ctx, id)
}

// List returns up to limit runs created before the run with the ID before,
// and the cursor of the next page, which is empty on the last page.
func (s *Service) List(ctx context.Context, before string, limit int) ([]models.SimulationRun, string, error) {
	items, err := s.store.List(ctx, before, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

// Stop asks a starting or running run to stop and cancels its worker when
// this process runs it. It returns the run as it is now.
func (s *Service) Stop(ctx context.Context, id, operatorID string) (models.SimulationRun, error) {
	stopped, err := s.store.RequestStop(ctx, id, operatorID)
	if err != nil {
		return models.SimulationRun{}, err
	}
	if stopped {
		s.mu.Lock()
		if cancel := s.cancels[id]; cancel != nil {
			cancel()
		}
		s.mu.Unlock()
	}
	return s.store.Get(ctx, id)
}

func validate(s *models.SimulationSettings) map[string]string {
	f := map[string]string{}
	if strings.TrimSpace(s.RequestID) != s.RequestID || len(s.RequestID) < 1 || len(s.RequestID) > 128 {
		f["request_id"] = "must contain 1 to 128 characters without surrounding whitespace"
	}
	if s.Mode != "live" && s.Mode != "backfill" {
		f["mode"] = "must be live or backfill"
	}
	if s.Seed > 9007199254740991 {
		f["seed"] = "must be a nonnegative safe integer"
	}
	if math.IsNaN(s.Rate) || math.IsInf(s.Rate, 0) || s.Rate < 1 || s.Rate > 50 {
		f["rate"] = "must be between 1 and 50"
	}
	if (s.Mode == "live" && s.Count != 0) || (s.Mode == "backfill" && (s.Count < 1 || s.Count > 10000)) {
		f["count"] = "backfill requires 1 to 10000 purchases; live requires zero"
	}
	if len(s.Scenarios) == 0 {
		f["scenarios"] = "select at least one scenario"
	}
	seen := map[simulator.Pattern]bool{}
	for _, p := range s.Scenarios {
		if !slices.Contains(simulator.Patterns, p) || seen[p] {
			f["scenarios"] = "contains an unknown or duplicate scenario"
		}
		seen[p] = true
	}
	slices.Sort(s.Scenarios)
	return f
}

// trackedScorer checkpoints each sequential purchase on the controller session.
type trackedScorer struct {
	client simulator.Scorer
	c      models.SimulationController
	id     string
	cancel context.CancelFunc
	fatal  error
	// failedAccountEvents counts the account events the client could not
	// send. The run sends purchases one at a time, so it needs no lock.
	failedAccountEvents int
}

// ScorePurchase saves the purchase's private label as pending, then scores
// it. A label that cannot be saved stops the run, because the run would
// otherwise have decisions without answers.
func (t *trackedScorer) ScorePurchase(ctx context.Context, p simulator.Purchase) (simulator.Result, error) {
	prep, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := t.c.PrepareLabel(prep, t.id, p)
	cancel()
	if err != nil {
		t.fatal = err
		t.cancel()
		return simulator.Result{}, simulator.FatalError{Err: err}
	}
	return t.Score(ctx, p.Request)
}

// SendAccountEvent forwards an account event to the client. Account events are
// not checkpointed: a run counts purchases only, and its purchase is scored
// even when the event fails. A failure is logged with the run ID and counted,
// so a run that finishes with failed events is visible. A client that cannot
// send account events skips them.
func (t *trackedScorer) SendAccountEvent(ctx context.Context, e simulator.AccountEvent) error {
	accounts, ok := t.client.(simulator.AccountEventSender)
	if !ok {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := accounts.SendAccountEvent(callCtx, e)
	if err != nil {
		t.failedAccountEvents++
		slog.Warn("simulation account event failed", "run_id", t.id, "event_id", e.EventID, "error", err)
	}
	return err
}

// reportAccountEventFailures logs how many account events the finished run
// could not send, if any.
func (t *trackedScorer) reportAccountEventFailures(status string) {
	if t.failedAccountEvents > 0 {
		slog.Warn("simulation run finished with failed account events",
			"run_id", t.id, "status", status, "failed_account_events", t.failedAccountEvents)
	}
}

// Score sends one purchase. It saves the transaction ID as pending before the
// request and counts the outcome after it, so an interrupted run can be
// reconciled. An error it cannot recover from is kept in fatal and cancels the
// run.
func (t *trackedScorer) Score(ctx context.Context, req simulator.Request) (simulator.Result, error) {
	if ctx.Err() != nil {
		return simulator.Result{}, ctx.Err()
	}
	// Stop scheduling promptly, but let an already dispatched request settle.
	callCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	running, err := t.c.MarkPending(callCtx, t.id, req.TransactionID)
	if err == nil && !running {
		t.cancel()
		return simulator.Result{}, context.Canceled
	}
	if err != nil {
		t.fatal = err
		t.cancel()
		return simulator.Result{}, err
	}
	result, err := t.client.Score(callCtx, req)
	if err != nil {
		if t.c.RejectLabel(callCtx, req.TransactionID, err) {
			t.fatal = err
			t.cancel()
			return simulator.Result{}, simulator.FatalError{Err: err}
		}
		// Transport errors can follow a committed decision. Reconcile by exact ID.
		lookupCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		outcome, lookupErr := t.c.OutcomeOf(lookupCtx, req.TransactionID)
		if lookupErr != nil {
			t.fatal = err
			t.cancel()
			return simulator.Result{}, err
		}
		result.Outcome = outcome
	}
	if result.Outcome != "allow" && result.Outcome != "review" && result.Outcome != "block" {
		t.fatal = fmt.Errorf("invalid score outcome")
		t.cancel()
		return simulator.Result{}, t.fatal
	}
	checkpointCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err = t.c.Checkpoint(checkpointCtx, t.id, req.TransactionID, result.Outcome); err != nil {
		t.fatal = err
		t.cancel()
	}
	return result, err
}

// sendRun sends the run's purchases through the scorer: live traffic until
// ctx is cancelled, or a backfill of generated purchases.
func sendRun(ctx context.Context, scorer simulator.Scorer, run models.SimulationRun) error {
	cfg := simulator.DefaultConfig()
	cfg.Run = run.ID
	cfg.Seed = run.Settings.Seed
	cfg.Scenarios = run.Settings.Scenarios
	cfg.Start = run.CreatedAt.UTC()
	if run.Settings.Mode == "live" {
		_, err := simulator.RunLive(ctx, scorer, cfg, run.Settings.Rate, nil)
		return err
	}
	purchases, err := simulator.GenerateContext(ctx, cfg, run.Settings.Count)
	if err != nil || len(purchases) == 0 {
		return err
	}
	endAt(purchases, run.CreatedAt)
	_, err = simulator.Run(ctx, scorer, purchases, run.Settings.Rate, nil)
	return err
}

// endAt shifts the purchases and their account events in time so that the
// last purchase happens at end. The score API rejects event times in the
// future, so a backfill ends when the run was created.
func endAt(purchases []simulator.Purchase, end time.Time) {
	shift := end.Sub(purchases[len(purchases)-1].Request.EventTime)
	for i := range purchases {
		p := &purchases[i].Request
		p.EventTime = p.EventTime.Add(shift)
		if p.AccountCreatedAt != nil {
			v := p.AccountCreatedAt.Add(shift)
			p.AccountCreatedAt = &v
		}
		for j := range purchases[i].AccountEvents {
			e := &purchases[i].AccountEvents[j]
			e.EventTime = e.EventTime.Add(shift)
		}
	}
}

func (s *Service) work(ctx context.Context, c models.SimulationController, run models.SimulationRun) {
	defer s.wg.Done()
	defer c.Close()
	defer func() { s.mu.Lock(); delete(s.cancels, run.ID); s.mu.Unlock() }()
	cancel := func() {
		s.mu.Lock()
		if f := s.cancels[run.ID]; f != nil {
			f()
		}
		s.mu.Unlock()
	}
	tracker := &trackedScorer{client: s.client, c: c, id: run.ID, cancel: cancel}
	err := sendRun(ctx, tracker, run)
	status, message := "completed", ""
	if ctx.Err() != nil {
		status = "stopped"
	}
	if tracker.fatal != nil || (err != nil && ctx.Err() == nil) {
		status = "failed"
		message = "simulation could not finish; check pending outcome"
	}
	finalCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err := c.Finish(finalCtx, run.ID, status, message); err != nil {
		slog.Error("simulation checkpoint unavailable", "run_id", run.ID, "error", err)
	}
	tracker.reportAccountEventFailures(status)
}
