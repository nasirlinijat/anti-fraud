package simulation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

type scoreFunc func(context.Context, simulator.Request) (simulator.Result, error)

func (f scoreFunc) Score(c context.Context, r simulator.Request) (simulator.Result, error) {
	return f(c, r)
}

func TestValidation(t *testing.T) {
	for _, s := range []models.SimulationSettings{{}, {RequestID: "a", Mode: "live", Rate: 51}, {RequestID: "a", Mode: "live", Rate: 1, Scenarios: []simulator.Pattern{"bad"}}, {RequestID: "a", Mode: "backfill", Rate: 1, Count: 10001, Scenarios: []simulator.Pattern{"normal"}}} {
		if len(validate(&s)) == 0 {
			t.Fatal("accepted invalid settings")
		}
	}
}

func TestStartRejectsInvalidSettingsBeforeLocking(t *testing.T) {
	s := &Service{store: nil, cancels: map[string]context.CancelFunc{}}
	_, _, err := s.Start(t.Context(), "op", models.SimulationSettings{RequestID: "a", Mode: "live", Rate: 51})
	var invalid *ValidationError
	if !errors.As(err, &invalid) || invalid.Fields["rate"] == "" || invalid.Fields["scenarios"] == "" {
		t.Fatalf("err = %v, want rate and scenarios problems", err)
	}
}

// testDSN returns the test database URL, or skips the test without one.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("AF77_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return dsn
}

// isolatedSchema creates a schema with all migrations applied and returns a
// pool that uses it, with the schema name. The schema is dropped when the
// test ends; the caller closes the pool.
func isolatedSchema(t *testing.T, dsn string) (*pgxpool.Pool, string) {
	t.Helper()
	admin, e := pgxpool.New(t.Context(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("simulations_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, e = admin.Exec(t.Context(), "CREATE SCHEMA "+ident); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, e := admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
		if e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	cfg := admin.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(t.Context(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	files, e := filepath.Glob("../../../migrations/*.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	if len(files) == 0 {
		t.Fatal("no migrations found")
	}
	for _, f := range files {
		body, e := os.ReadFile(f)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(t.Context(), string(body)); e != nil {
			t.Fatalf("%s: %v", f, e)
		}
	}
	return pool, schema
}

// withSearchPath returns the database URL with the schema as its search path.
func withSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// runtimePool returns a pool and URL for the restricted runtime role on the
// schema, when a runtime database URL is set. Without one it returns the
// owner pool and the owner URL.
func runtimePool(t *testing.T, pool *pgxpool.Pool, ownerDSN, schema string) (*pgxpool.Pool, string) {
	t.Helper()
	runtimeDSN := os.Getenv("AF77_RUNTIME_TEST_DATABASE_URL")
	if runtimeDSN == "" {
		runtimeDSN = os.Getenv("TEST_RUNTIME_DATABASE_URL")
	}
	if runtimeDSN == "" {
		return pool, withSearchPath(t, ownerDSN, schema)
	}
	ident := pgx.Identifier{schema}.Sanitize()
	if _, e := pool.Exec(t.Context(), "GRANT USAGE ON SCHEMA "+ident+" TO antifraud_api; GRANT SELECT ON purchases,decisions TO antifraud_api; GRANT SELECT,INSERT,UPDATE ON simulation_runs TO antifraud_api"); e != nil {
		t.Fatal(e)
	}
	rc, e := pgxpool.ParseConfig(runtimeDSN)
	if e != nil {
		t.Fatal(e)
	}
	rc.ConnConfig.RuntimeParams["search_path"] = schema
	runtime, e := pgxpool.NewWithConfig(t.Context(), rc)
	if e != nil {
		t.Fatal(e)
	}
	return runtime, withSearchPath(t, runtimeDSN, schema)
}

func setup(t *testing.T, scorer simulator.Scorer) (*Service, *storage.SimulationStore, *pgxpool.Pool) {
	t.Helper()
	ownerDSN := testDSN(t)
	pool, schema := isolatedSchema(t, ownerDSN)
	runtime, dsn := runtimePool(t, pool, ownerDSN, schema)
	store := storage.NewSimulationStore(runtime, dsn)
	s, e := NewService(t.Context(), store, scorer)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := s.Close(ctx); e != nil {
			t.Error(e)
		}
		if runtime != pool {
			runtime.Close()
		}
		pool.Close()
	})
	return s, store, pool
}

// settings decodes a start request body over the defaults, the way the
// handler does.
func settings(t *testing.T, body string) models.SimulationSettings {
	t.Helper()
	s := DefaultSettings()
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func wait(t *testing.T, s *Service, id string) models.SimulationRun {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
		r, e := s.Get(t.Context(), id)
		if e != nil {
			t.Fatal(e)
		}
		if r.Status != "starting" && r.Status != "running" && r.Status != "stopping" {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not finish")
	return models.SimulationRun{}
}

// heldScorer returns a scorer that closes entered on its first call and
// answers "review" once release is closed.
func heldScorer(entered chan<- struct{}, release <-chan struct{}) simulator.Scorer {
	var once sync.Once
	return scoreFunc(func(ctx context.Context, r simulator.Request) (simulator.Result, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return simulator.Result{Outcome: "review"}, nil
		case <-ctx.Done():
			return simulator.Result{}, ctx.Err()
		}
	})
}

func TestDurableLifecycle(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s, store, _ := setup(t, heldScorer(entered, release))
	one := settings(t, `{"request_id":"one","mode":"live","rate":50}`)
	run, created, err := s.Start(t.Context(), "op", one)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	<-entered
	again, created, err := s.Start(t.Context(), "op", settings(t, `{"request_id":"one","mode":"live","rate":50}`))
	if err != nil || created || again.ID != run.ID {
		t.Fatal("retry did not return the same run", created, err)
	}
	var active *ActiveError
	if _, _, err := s.Start(t.Context(), "op", settings(t, `{"request_id":"two","mode":"live","rate":5}`)); !errors.As(err, &active) || active.RunID != run.ID {
		t.Fatalf("err = %v, want the active run %s", err, run.ID)
	}
	if _, _, err := s.Start(t.Context(), "op", settings(t, `{"request_id":"one","mode":"live","rate":6}`)); !errors.Is(err, ErrRequestReused) {
		t.Fatalf("err = %v, want ErrRequestReused", err)
	}
	other, e := NewService(t.Context(), store, s.client)
	if e != nil {
		t.Fatal(e)
	}
	current, e := other.Get(t.Context(), run.ID)
	if e != nil || current.Status == "interrupted" {
		t.Fatal("stole active worker", e)
	}
	stopping, err := s.Stop(t.Context(), run.ID, "op")
	if err != nil || stopping.Status != "stopping" {
		t.Fatal(stopping.Status, err)
	}
	close(release)
	r := wait(t, s, run.ID)
	if r.Status != "stopped" || r.Sent != 1 || r.Reviewed != 1 || r.Pending {
		t.Fatalf("bad final snapshot %+v", r)
	}
	if stopped, err := s.Stop(t.Context(), run.ID, "op"); err != nil || stopped.Status != "stopped" {
		t.Fatal(stopped.Status, err)
	}
	if _, _, err := s.List(t.Context(), "", 20); err != nil {
		t.Fatal(err)
	}
	if done, created, err := s.Start(t.Context(), "op", one); err != nil || created || done.ID != run.ID {
		t.Fatal(created, err)
	}
}

func TestBackfillAndConcurrentStarts(t *testing.T) {
	var mu sync.Mutex
	var latest time.Time
	s, _, _ := setup(t, scoreFunc(func(_ context.Context, r simulator.Request) (simulator.Result, error) {
		mu.Lock()
		if r.EventTime.After(latest) {
			latest = r.EventTime
		}
		mu.Unlock()
		return simulator.Result{Outcome: "allow"}, nil
	}))
	type started struct {
		run     models.SimulationRun
		created bool
		err     error
	}
	var wg sync.WaitGroup
	results := make(chan started, 8)
	for i := range 8 {
		wg.Go(func() {
			run, created, err := s.Start(t.Context(), "op", settings(t, fmt.Sprintf(`{"request_id":"c%d","mode":"backfill","count":20,"rate":50,"scenarios":["card_testing"]}`, i)))
			results <- started{run, created, err}
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	var id string
	for res := range results {
		var active *ActiveError
		switch {
		case res.err == nil && res.created:
			accepted++
			id = res.run.ID
		case !errors.As(res.err, &active):
			t.Fatal(res.created, res.err)
		}
	}
	if accepted != 1 {
		t.Fatal("accepted", accepted)
	}
	r := wait(t, s, id)
	if r.Status != "completed" || r.Sent != 20 || r.Allowed != 20 {
		t.Fatalf("%+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if !latest.Equal(r.CreatedAt) {
		t.Fatalf("backfill ends at %v, want the run's creation time %v", latest, r.CreatedAt)
	}
}

func TestRecoveryCommitGap(t *testing.T) {
	s, store, pool := setup(t, scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) { return simulator.Result{}, nil }))
	_, e := pool.Exec(t.Context(), `INSERT INTO simulation_runs(id,operator_id,request_id,settings,status,pending_transaction_id) VALUES('orphan','op','recover','{"request_id":"recover","mode":"live","rate":5,"seed":1,"count":0,"scenarios":["normal"]}','running','af77-recover-txn')`)
	if e != nil {
		t.Fatal(e)
	}
	var version int
	if e = pool.QueryRow(t.Context(), `SELECT version FROM rulesets WHERE is_active`).Scan(&version); e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(t.Context(), `WITH p AS (INSERT INTO purchases(transaction_id,event_time,amount_minor,player_id,card_token) VALUES('af77-recover-txn',now(),100,'af77-p','af77-c') RETURNING id) INSERT INTO decisions(purchase_id,ruleset_version,outcome,rule_score,fired_rules,model_inputs) SELECT id,$1,'block',90,'[]','{}' FROM p`, version)
	if e != nil {
		t.Fatal(e)
	}

	c, ok, e := store.Lock(t.Context())
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	defer c.Close()
	for range 2 {
		if e = c.Recover(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	r, e := s.Get(t.Context(), "orphan")
	if e != nil || r.Status != "interrupted" || r.Sent != 1 || r.Blocked != 1 || r.Pending {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestFailureAndHistory(t *testing.T) {
	s, _, _ := setup(t, scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
		return simulator.Result{}, fmt.Errorf("transport unavailable")
	}))
	run, created, err := s.Start(t.Context(), "op", settings(t, `{"request_id":"failure","mode":"live"}`))
	if err != nil || !created {
		t.Fatal(created, err)
	}
	r := wait(t, s, run.ID)
	if r.Status != "failed" || r.Failed != 1 || !r.Pending || r.Sent != 0 {
		t.Fatalf("%+v", r)
	}
	if items, next, err := s.List(t.Context(), "", 1); err != nil || len(items) != 1 || next != "" {
		t.Fatal(len(items), next, err)
	}
	if _, _, err := s.List(t.Context(), "unknown", 20); !errors.Is(err, models.ErrInvalidSimulationCursor) {
		t.Fatalf("err = %v, want ErrInvalidSimulationCursor", err)
	}
	if _, err := s.Get(t.Context(), "unknown"); !errors.Is(err, models.ErrSimulationNotFound) {
		t.Fatalf("err = %v, want ErrSimulationNotFound", err)
	}
}

func TestControllerLossStopsDispatch(t *testing.T) {
	var pool *pgxpool.Pool
	calls := 0
	s, store, p := setup(t, scoreFunc(func(ctx context.Context, req simulator.Request) (simulator.Result, error) {
		calls++
		_, e := pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND classid=27149 AND objid=77 AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`)
		if e != nil {
			return simulator.Result{}, e
		}
		return simulator.Result{Outcome: "allow"}, nil
	}))
	pool = p
	run, created, err := s.Start(t.Context(), "op", settings(t, `{"request_id":"connection-loss","mode":"live","rate":50}`))
	if err != nil || !created {
		t.Fatal(created, err)
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker continued after controller loss")
	}
	if calls != 1 {
		t.Fatal("dispatched after losing ownership", calls)
	}
	recovered, e := NewService(t.Context(), store, s.client)
	if e != nil {
		t.Fatal(e)
	}
	r, e := recovered.Get(t.Context(), run.ID)
	if e != nil || r.Status != "interrupted" || !r.Pending {
		t.Fatalf("%+v %v", r, e)
	}
}
