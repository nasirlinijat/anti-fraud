package simulations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

type scoreFunc func(context.Context, simulator.Request) (simulator.Result, error)

func (f scoreFunc) Score(c context.Context, r simulator.Request) (simulator.Result, error) {
	return f(c, r)
}
func TestValidation(t *testing.T) {
	for _, s := range []Settings{{}, {RequestID: "a", Mode: "live", Rate: 51}, {RequestID: "a", Mode: "live", Rate: 1, Scenarios: []simulator.Pattern{"bad"}}, {RequestID: "a", Mode: "backfill", Rate: 1, Count: 10001, Scenarios: []simulator.Pattern{"normal"}}} {
		if len(s.validate()) == 0 {
			t.Fatal("accepted invalid settings")
		}
	}
}
func TestDisabled(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil, false, "key", platform.OperatorKeys{})
	for _, path := range []string{"/simulations", "/simulations/x", "/simulations/x/stop"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(w.Code)
		}
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
	files, e := filepath.Glob("../../migrations/*.up.sql")
	if e != nil {
		t.Fatal(e)
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

func setup(t *testing.T, scorer simulator.Scorer) (*Manager, *http.ServeMux, *pgxpool.Pool) {
	t.Helper()
	ownerDSN := testDSN(t)
	pool, schema := isolatedSchema(t, ownerDSN)
	runtime, dsn := runtimePool(t, pool, ownerDSN, schema)
	m, e := New(t.Context(), runtime, dsn, scorer)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := m.Close(ctx); e != nil {
			t.Error(e)
		}
		if runtime != pool {
			runtime.Close()
		}
		pool.Close()
	})
	ops, e := platform.ParseOperatorKeys(`[{"operator_id":"op","key":"operator"}]`, "service", platform.AnalystKeys{})
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, m, true, "service", ops)
	return m, mux, pool
}

func call(mux *http.ServeMux, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if key != "" {
		r.Header.Set("X-API-Key", key)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func decode(t *testing.T, w *httptest.ResponseRecorder) Run {
	t.Helper()
	var r Run
	if e := json.Unmarshal(w.Body.Bytes(), &r); e != nil {
		t.Fatal(e, w.Body.String())
	}
	return r
}
func wait(t *testing.T, m *Manager, id string) Run {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
		r, e := m.get(t.Context(), id)
		if e != nil {
			t.Fatal(e)
		}
		if r.Status != "starting" && r.Status != "running" && r.Status != "stopping" {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not finish")
	return Run{}
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
	m, mux, _ := setup(t, heldScorer(entered, release))
	body := `{"request_id":"one","mode":"live","rate":50}`
	for _, key := range []string{"", "service", "bad"} {
		if w := call(mux, "POST", "/simulations", key, body); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	w := call(mux, "POST", "/simulations", "operator", body)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	run := decode(t, w)
	<-entered
	for _, key := range []string{"operator", "bad", ""} {
		if w := call(mux, "GET", "/simulations/"+run.ID, key, ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := call(mux, "POST", "/simulations", "operator", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(mux, "POST", "/simulations", "operator", `{"request_id":"two","mode":"live","rate":5}`); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(mux, "POST", "/simulations", "operator", `{"request_id":"one","mode":"live","rate":6}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	other, e := New(t.Context(), m.pool, m.dsn, m.client)
	if e != nil {
		t.Fatal(e)
	}
	current, e := other.get(t.Context(), run.ID)
	if e != nil || current.Status == "interrupted" {
		t.Fatal("stole active worker", e)
	}
	if w := call(mux, "POST", "/simulations/"+run.ID+"/stop", "operator", ""); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	close(release)
	r := wait(t, m, run.ID)
	if r.Status != "stopped" || r.Sent != 1 || r.Reviewed != 1 || r.Pending {
		t.Fatalf("bad final snapshot %+v", r)
	}
	if w := call(mux, "POST", "/simulations/"+run.ID+"/stop", "operator", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call(mux, "GET", "/simulations", "service", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(mux, "POST", "/simulations", "operator", body); w.Code != 200 || decode(t, w).ID != run.ID {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestBackfillAndConcurrentStarts(t *testing.T) {
	m, mux, _ := setup(t, scoreFunc(func(_ context.Context, r simulator.Request) (simulator.Result, error) {
		if r.EventTime.After(time.Now()) {
			return simulator.Result{}, fmt.Errorf("future backfill")
		}
		return simulator.Result{Outcome: "allow"}, nil
	}))
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 8)
	for i := range 8 {
		wg.Go(func() {
			responses <- call(mux, "POST", "/simulations", "operator", fmt.Sprintf(`{"request_id":"c%d","mode":"backfill","count":20,"rate":50,"scenarios":["card_testing"]}`, i))
		})
	}
	wg.Wait()
	close(responses)
	accepted := 0
	var id string
	for w := range responses {
		if w.Code == 202 {
			accepted++
			id = decode(t, w).ID
		} else if w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if accepted != 1 {
		t.Fatal("accepted", accepted)
	}
	r := wait(t, m, id)
	if r.Status != "completed" || r.Sent != 20 || r.Allowed != 20 {
		t.Fatalf("%+v", r)
	}
}
func TestRecoveryCommitGap(t *testing.T) {
	m, _, pool := setup(t, scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) { return simulator.Result{}, nil }))
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

	c, ok, e := m.lock(t.Context())
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	defer c.Close(context.Background())
	for range 2 {
		if e = m.recover(t.Context(), c); e != nil {
			t.Fatal(e)
		}
	}
	r, e := m.get(t.Context(), "orphan")
	if e != nil || r.Status != "interrupted" || r.Sent != 1 || r.Blocked != 1 || r.Pending {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestFailureAndHistory(t *testing.T) {
	m, mux, _ := setup(t, scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
		return simulator.Result{}, fmt.Errorf("transport unavailable")
	}))
	for _, body := range []string{`{`, `null`, `{"mode":"live","request_id":"bad","unknown":1}`, `{"mode":"live","request_id":"bad"} {}`} {
		if w := call(mux, "POST", "/simulations", "operator", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := call(mux, "POST", "/simulations", "operator", `{"request_id":"failure","mode":"live"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := wait(t, m, decode(t, w).ID)
	if r.Status != "failed" || r.Failed != 1 || !r.Pending || r.Sent != 0 {
		t.Fatalf("%+v", r)
	}
	if w := call(mux, "GET", "/simulations?limit=1", "service", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/simulations?limit=0", "/simulations?before=unknown"} {
		if w := call(mux, "GET", path, "service", ""); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := call(mux, "GET", "/simulations/unknown", "service", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestControllerLossStopsDispatch(t *testing.T) {
	var pool *pgxpool.Pool
	calls := 0
	m, _, p := setup(t, scoreFunc(func(ctx context.Context, req simulator.Request) (simulator.Result, error) {
		calls++
		_, e := pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND classid=27149 AND objid=77 AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`)
		if e != nil {
			return simulator.Result{}, e
		}
		return simulator.Result{Outcome: "allow"}, nil
	}))
	pool = p
	ops, _ := platform.ParseOperatorKeys(`[{"operator_id":"op","key":"operator"}]`, "service", platform.AnalystKeys{})
	mux := http.NewServeMux()
	RegisterRoutes(mux, m, true, "service", ops)
	w := call(mux, "POST", "/simulations", "operator", `{"request_id":"connection-loss","mode":"live","rate":50}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := decode(t, w).ID
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker continued after controller loss")
	}
	if calls != 1 {
		t.Fatal("dispatched after losing ownership", calls)
	}
	recovered, e := New(t.Context(), m.pool, m.dsn, m.client)
	if e != nil {
		t.Fatal(e)
	}
	r, e := recovered.get(t.Context(), id)
	if e != nil || r.Status != "interrupted" || !r.Pending {
		t.Fatalf("%+v %v", r, e)
	}
}
