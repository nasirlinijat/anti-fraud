// Package simulations controls durable, sequential console simulation runs.
package simulations

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

// Settings is the normalized, immutable request used for start retries.
type Settings struct {
	RequestID string              `json:"request_id"`
	Mode      string              `json:"mode"`
	Scenarios []simulator.Pattern `json:"scenarios"`
	Seed      uint64              `json:"seed"`
	Rate      float64             `json:"rate"`
	Count     int                 `json:"count"`
}

func (s *Settings) validate() map[string]string {
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

// Run is a durable snapshot. Pending indicates an outcome not yet checkpointed.
type Run struct {
	ID         string     `json:"id"`
	Settings   Settings   `json:"settings"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Sent       int64      `json:"sent"`
	Failed     int64      `json:"failed"`
	Allowed    int64      `json:"allowed"`
	Reviewed   int64      `json:"reviewed"`
	Blocked    int64      `json:"blocked"`
	Pending    bool       `json:"pending"`
	Error      string     `json:"error,omitempty"`
}

const columns = `id, settings, status, created_at, finished_at, sent, failed, allowed, reviewed, blocked, pending_transaction_id IS NOT NULL, error`

func scan(row pgx.Row) (Run, error) {
	var r Run
	var settings []byte
	err := row.Scan(&r.ID, &settings, &r.Status, &r.CreatedAt, &r.FinishedAt, &r.Sent, &r.Failed, &r.Allowed, &r.Reviewed, &r.Blocked, &r.Pending, &r.Error)
	if err == nil {
		err = json.Unmarshal(settings, &r.Settings)
	}
	r.CreatedAt = r.CreatedAt.UTC()
	if r.FinishedAt != nil {
		v := r.FinishedAt.UTC()
		r.FinishedAt = &v
	}
	return r, err
}

// Manager owns background workers; database locks also exclude other processes.
type Manager struct {
	pool    *pgxpool.Pool
	dsn     string
	client  simulator.Scorer
	mu      sync.Mutex
	closed  bool
	cancels map[string]context.CancelFunc
	wg      sync.WaitGroup
}

func New(ctx context.Context, pool *pgxpool.Pool, dsn string, client simulator.Scorer) (*Manager, error) {
	m := &Manager{pool: pool, dsn: dsn, client: client, cancels: map[string]context.CancelFunc{}}
	c, ok, err := m.lock(ctx)
	if err != nil {
		return nil, err
	}
	if ok {
		defer c.Close(context.Background())
		err = m.recover(ctx, c)
	}
	return m, err
}

// lock uses a dedicated connection: a pool release must never retain a session lock.
func (m *Manager) lock(ctx context.Context) (*pgx.Conn, bool, error) {
	c, err := pgx.Connect(ctx, m.dsn)
	if err != nil {
		return nil, false, err
	}
	var ok bool
	err = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(27149,77)`).Scan(&ok)
	if err != nil || !ok {
		c.Close(ctx)
		return nil, false, err
	}
	return c, true, nil
}
func (m *Manager) recover(ctx context.Context, c *pgx.Conn) error {
	// A single sequential worker writes its pending ID before sending. Recover the
	// commit/checkpoint gap by exact transaction lookup, never by a prefix scan.
	_, err := c.Exec(ctx, `UPDATE simulation_runs r SET
 sent=r.sent+1, allowed=r.allowed+(d.outcome='allow')::int,
 reviewed=r.reviewed+(d.outcome='review')::int, blocked=r.blocked+(d.outcome='block')::int,
 pending_transaction_id=NULL, failed=GREATEST(r.failed-1,0)
 FROM purchases p JOIN decisions d ON d.purchase_id=p.id
 WHERE p.transaction_id=r.pending_transaction_id`)
	if err != nil {
		return err
	}
	_, err = c.Exec(ctx, `UPDATE simulation_runs SET status='interrupted', finished_at=now(), error='backend interrupted the run'
 WHERE status IN ('starting','running','stopping')`)
	return err
}
func (m *Manager) get(ctx context.Context, id string) (Run, error) {
	return scan(m.pool.QueryRow(ctx, `SELECT `+columns+` FROM simulation_runs WHERE id=$1`, id))
}

// Close rejects starts and drains in-flight work within the caller's budget.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	for _, cancel := range m.cancels {
		cancel()
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RegisterRoutes registers nothing when simulations are disabled.
func bounded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RegisterRoutes(mux *http.ServeMux, m *Manager, enabled bool, key string, operators platform.OperatorKeys) {
	if !enabled {
		return
	}
	mux.Handle("POST /simulations", bounded(platform.RequireOperator(operators, m.start)))
	mux.Handle("POST /simulations/{id}/stop", bounded(platform.RequireOperator(operators, m.stop)))
	mux.Handle("GET /simulations/{id}", bounded(platform.RequireAPIKey(key, http.HandlerFunc(m.read))))
	mux.Handle("GET /simulations", bounded(platform.RequireAPIKey(key, http.HandlerFunc(m.list))))
}
func failure(w http.ResponseWriter, err error) {
	log.Printf("simulation operation: %v", err)
	platform.WriteError(w, 503, "simulation service unavailable")
}
func (m *Manager) start(w http.ResponseWriter, r *http.Request, operator string) {
	s := Settings{Seed: 1, Rate: 5, Scenarios: slices.Clone(simulator.Patterns)}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		platform.WriteError(w, 400, "invalid simulation request")
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		platform.WriteError(w, 400, "expected one JSON object")
		return
	}
	if s.Mode == "backfill" && s.Count == 0 {
		s.Count = 500
	}
	if fields := s.validate(); len(fields) > 0 {
		platform.WriteJSON(w, 400, platform.ErrorResponse{Error: "invalid request", Fields: fields})
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		platform.WriteError(w, 503, "simulation service is shutting down")
		return
	}
	old, err := scan(m.pool.QueryRow(r.Context(), `SELECT `+columns+` FROM simulation_runs WHERE operator_id=$1 AND request_id=$2`, operator, s.RequestID))
	if err == nil {
		if !reflect.DeepEqual(old.Settings, s) {
			platform.WriteError(w, 409, "request_id was used with different settings")
			return
		}
		platform.WriteJSON(w, 200, old)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		failure(w, err)
		return
	}
	c, ok, err := m.lock(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	if !ok {
		active, e := scan(m.pool.QueryRow(r.Context(), `SELECT `+columns+` FROM simulation_runs WHERE status IN ('starting','running','stopping')`))
		if e != nil {
			platform.WriteError(w, 409, "another simulation controller is active")
			return
		}
		platform.WriteJSON(w, 409, struct {
			Error       string `json:"error"`
			ActiveRunID string `json:"active_run_id"`
		}{"a simulation is already active", active.ID})
		return
	}
	keep := false
	defer func() {
		if !keep {
			c.Close(context.Background())
		}
	}()
	if err = m.recover(r.Context(), c); err != nil {
		failure(w, err)
		return
	}
	// Another process may have committed this idempotency key before releasing ownership.
	old, err = scan(c.QueryRow(r.Context(), `SELECT `+columns+` FROM simulation_runs WHERE operator_id=$1 AND request_id=$2`, operator, s.RequestID))
	if err == nil {
		if !reflect.DeepEqual(old.Settings, s) {
			platform.WriteError(w, 409, "request_id was used with different settings")
			return
		}
		platform.WriteJSON(w, 200, old)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		failure(w, err)
		return
	}
	body, _ := json.Marshal(s)
	id := "sim-" + rand.Text()
	run, err := scan(c.QueryRow(r.Context(), `INSERT INTO simulation_runs(id,operator_id,request_id,settings,status) VALUES($1,$2,$3,$4,'starting') RETURNING `+columns, id, operator, s.RequestID, body))
	if err != nil {
		failure(w, err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancels[id] = cancel
	m.wg.Add(1)
	keep = true
	go m.work(ctx, c, run)
	w.Header().Set("Location", "/simulations/"+id)
	platform.WriteJSON(w, 202, run)
}
func (m *Manager) read(w http.ResponseWriter, r *http.Request) {
	run, err := m.get(r.Context(), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		platform.WriteError(w, 404, "simulation not found")
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	platform.WriteJSON(w, 200, run)
}
func (m *Manager) list(w http.ResponseWriter, r *http.Request) {
	cursor := r.URL.Query().Get("before")
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 100 {
			platform.WriteError(w, 400, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	var before time.Time
	if cursor != "" {
		e := m.pool.QueryRow(r.Context(), `SELECT created_at FROM simulation_runs WHERE id=$1`, cursor).Scan(&before)
		if e != nil {
			if !errors.Is(e, pgx.ErrNoRows) {
				failure(w, e)
				return
			}
			platform.WriteError(w, 400, "invalid history cursor")
			return
		}
	}
	rows, err := m.pool.Query(r.Context(), `SELECT `+columns+` FROM simulation_runs WHERE $1='' OR (created_at,id)<($2,$1) ORDER BY created_at DESC,id DESC LIMIT $3`, cursor, before, limit+1)
	if err != nil {
		failure(w, err)
		return
	}
	defer rows.Close()
	items := []Run{}
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			failure(w, e)
			return
		}
		items = append(items, v)
	}
	if rows.Err() != nil {
		failure(w, rows.Err())
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	platform.WriteJSON(w, 200, struct {
		Items     []Run               `json:"items"`
		Next      string              `json:"next_cursor,omitempty"`
		Scenarios []simulator.Pattern `json:"available_scenarios"`
	}{items, next, simulator.Patterns})
}
func (m *Manager) stop(w http.ResponseWriter, r *http.Request, operator string) {
	tag, err := m.pool.Exec(r.Context(), `UPDATE simulation_runs SET status='stopping',stop_requested_by=$2 WHERE id=$1 AND status IN ('starting','running')`, r.PathValue("id"), operator)
	if err != nil {
		failure(w, err)
		return
	}
	if tag.RowsAffected() > 0 {
		m.mu.Lock()
		if cancel := m.cancels[r.PathValue("id")]; cancel != nil {
			cancel()
		}
		m.mu.Unlock()
	}
	run, err := m.get(r.Context(), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		platform.WriteError(w, 404, "simulation not found")
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	status := 200
	if run.Status == "stopping" {
		status = 202
	}
	platform.WriteJSON(w, status, run)
}

// trackedScorer checkpoints each sequential purchase on the controller session.
type trackedScorer struct {
	m      *Manager
	c      *pgx.Conn
	id     string
	cancel context.CancelFunc
	fatal  error
}

func (t *trackedScorer) Score(ctx context.Context, req simulator.Request) (simulator.Result, error) {
	if ctx.Err() != nil {
		return simulator.Result{}, ctx.Err()
	}
	// Stop scheduling promptly, but let an already dispatched request settle.
	callCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tag, err := t.c.Exec(callCtx, `UPDATE simulation_runs SET pending_transaction_id=$2,status='running' WHERE id=$1 AND status IN ('starting','running')`, t.id, req.TransactionID)
	if err == nil && tag.RowsAffected() == 0 {
		t.cancel()
		return simulator.Result{}, context.Canceled
	}
	if err != nil {
		t.fatal = err
		t.cancel()
		return simulator.Result{}, err
	}
	result, err := t.m.client.Score(callCtx, req)
	if err != nil {
		// Transport errors can follow a committed decision. Reconcile by exact ID.
		lookupCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		lookupErr := t.c.QueryRow(lookupCtx, `SELECT d.outcome FROM purchases p JOIN decisions d ON d.purchase_id=p.id WHERE p.transaction_id=$1`, req.TransactionID).Scan(&result.Outcome)
		if lookupErr != nil {
			t.fatal = err
			t.cancel()
			return simulator.Result{}, err
		}
	}
	if result.Outcome != "allow" && result.Outcome != "review" && result.Outcome != "block" {
		t.fatal = fmt.Errorf("invalid score outcome")
		t.cancel()
		return simulator.Result{}, t.fatal
	}
	checkpointCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_, err = t.c.Exec(checkpointCtx, `UPDATE simulation_runs SET sent=sent+1,allowed=allowed+($3='allow')::int,reviewed=reviewed+($3='review')::int,blocked=blocked+($3='block')::int,pending_transaction_id=NULL WHERE id=$1 AND pending_transaction_id=$2`, t.id, req.TransactionID, result.Outcome)
	if err != nil {
		t.fatal = err
		t.cancel()
	}
	return result, err
}
func (m *Manager) work(ctx context.Context, c *pgx.Conn, run Run) {
	defer m.wg.Done()
	defer c.Close(context.Background())
	defer func() { m.mu.Lock(); delete(m.cancels, run.ID); m.mu.Unlock() }()
	cancel := func() {
		m.mu.Lock()
		if f := m.cancels[run.ID]; f != nil {
			f()
		}
		m.mu.Unlock()
	}
	cfg := simulator.DefaultConfig()
	cfg.Run = run.ID
	cfg.Seed = run.Settings.Seed
	cfg.Scenarios = run.Settings.Scenarios
	cfg.Start = run.CreatedAt.UTC()
	tracker := &trackedScorer{m: m, c: c, id: run.ID, cancel: cancel}
	var err error
	if run.Settings.Mode == "live" {
		_, err = simulator.RunLive(ctx, tracker, cfg, run.Settings.Rate, nil)
	} else {
		var purchases []simulator.Purchase
		purchases, err = simulator.GenerateContext(ctx, cfg, run.Settings.Count)
		if err == nil && len(purchases) > 0 {
			shift := run.CreatedAt.Sub(purchases[len(purchases)-1].Request.EventTime)
			for i := range purchases {
				p := &purchases[i].Request
				p.EventTime = p.EventTime.Add(shift)
				if p.AccountCreatedAt != nil {
					v := p.AccountCreatedAt.Add(shift)
					p.AccountCreatedAt = &v
				}
			}
			_, err = simulator.Run(ctx, tracker, purchases, run.Settings.Rate, nil)
		}
	}
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
	_, saveErr := c.Exec(finalCtx, `UPDATE simulation_runs SET status=CASE WHEN status='stopping' AND $2='completed' THEN 'stopped' ELSE $2 END,finished_at=now(),error=$3,failed=failed+($2='failed')::int WHERE id=$1 AND status IN ('starting','running','stopping')`, run.ID, status, message)
	if saveErr != nil {
		log.Printf("simulation checkpoint unavailable: %v", saveErr)
	}
}
