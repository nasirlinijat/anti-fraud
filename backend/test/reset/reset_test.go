//go:build integration

package reset_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
	"github.com/nasirlinijat/anti-fraud/backend/test/testdb"
	"github.com/redis/go-redis/v9"
)

func pool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestResetClearsStateAndScoringWorks(t *testing.T) {
	db, err := testdb.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Stop()
	owner, runtime, evaluation := pool(t, db.OwnerURL), pool(t, db.RuntimeURL), pool(t, db.LabelsURL)
	resetter, err := labels.NewResetStore(t.Context(), runtime, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewSimulationStore(runtime, db.RuntimeURL)
	manager, err := simulation.NewService(t.Context(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	seed(t, owner)
	assertPermission(t, runtime)
	assertRollback(t, owner)
	client, cache, outage := resetCache(t, runtime)
	client.Set(t.Context(), "unrelated", "keep", 0)
	manager.UseReset(resetter, cache)
	held, ok, err := store.Lock(t.Context())
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := manager.Reset(t.Context(), "op"); err == nil {
		t.Fatal("reset accepted active controller")
	}
	held.Close()
	if err := manager.Reset(t.Context(), "op"); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, owner)
	if value := client.Get(t.Context(), "unrelated").Val(); value != "keep" {
		t.Fatal("unrelated cache data removed")
	}
	assertFreshScore(t, runtime, cache)
	if err := manager.Reset(t.Context(), "op"); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, owner)
	testUnavailableCache(t, outage, owner, manager, cache)
	assertNewSimulation(t, runtime, evaluation, db.RuntimeURL)
}

func seed(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	_, err := p.Exec(t.Context(), `INSERT INTO accounts(username,password_hash,role) VALUES('reset-admin','hash','admin');
 INSERT INTO sessions(token_hash,account_id,expires_at) SELECT sha256('token'),id,now()+interval '1 day' FROM accounts;
 WITH p AS (INSERT INTO purchases(transaction_id,event_time,amount_minor,player_id,card_token) VALUES('old',now()-interval '1 minute',100,'player','card') RETURNING id)
 INSERT INTO decisions(purchase_id,ruleset_version,outcome,rule_score,fired_rules,model_inputs) SELECT id,1,'review',50,'[]','{}' FROM p;
 INSERT INTO review_claims SELECT purchase_id,(SELECT id FROM accounts LIMIT 1),now() FROM decisions;
 INSERT INTO review_resolutions SELECT purchase_id,'approve','analyst',now() FROM decisions;
 INSERT INTO purchase_labels(transaction_id,run_id,source,scenario,is_fraud) VALUES('old','run','simulator','normal',false);
 INSERT INTO pending_purchase_labels(transaction_id,run_id,source,is_fraud,rejection_reason) VALUES('pending','run','ieee-cis',true,'quarantined');
 INSERT INTO feature_counts VALUES('all','',1,1);
 INSERT INTO blocklist_entries(entry_type,value,reason) VALUES('card','card','demo');
 INSERT INTO account_events(event_id,player_id,event_type,event_time,device_id,ip_country) VALUES('event','player','login',now()-interval '1 hour','device','AZ');
 INSERT INTO chargebacks SELECT 'cb',id,'other',100,now(),now() FROM purchases;
 INSERT INTO simulation_runs(id,operator_id,request_id,settings,status) VALUES('old-run','op','old-request','{}','completed');
 INSERT INTO configuration_audit_events(actor_kind,actor_id,action,resource_id) VALUES('operator','op','blocklist.add',1);`)
	if err != nil {
		t.Fatal(err)
	}
}

var resetTables = []string{"purchases", "decisions", "review_claims", "review_resolutions", "purchase_labels", "pending_purchase_labels", "feature_counts", "blocklist_entries", "account_events", "chargebacks", "simulation_runs"}

func assertEmpty(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	for _, table := range resetTables {
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", table, n, err)
		}
	}
	for _, table := range []string{"rulesets", "ruleset_rules", "accounts", "sessions", "configuration_audit_events"} {
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n == 0 {
			t.Fatalf("preserve %s: %d %v", table, n, err)
		}
	}
}

func assertPermission(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	if _, err := p.Exec(t.Context(), "SELECT reset_demo_data()"); err == nil {
		t.Fatal("runtime can reset")
	}
}

func assertRollback(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "CREATE TEMP TABLE purchases (marker int); INSERT INTO purchases VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "SELECT reset_demo_data()"); err != nil {
		t.Fatal(err)
	}
	var marker int
	if err := tx.QueryRow(t.Context(), "SELECT marker FROM pg_temp.purchases").Scan(&marker); err != nil || marker != 1 {
		t.Fatal("helper followed caller search path", err)
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range resetTables {
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n == 0 {
			t.Fatalf("rollback %s: %d %v", table, n, err)
		}
	}
}

func assertFreshScore(t *testing.T, p *pgxpool.Pool, cache *storage.CachedBlocklistStore) {
	t.Helper()
	if _, found, err := cache.Match(t.Context(), models.BlocklistSubject{CardToken: "card", PlayerID: "player"}); err != nil || found {
		t.Fatal("stale block", found, err)
	}
	model, err := lgbm.Load("../../models/lgbm-game-v9.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := lgbm.LoadPolicy("../../models/lgbm-game-v9.report.json")
	if err != nil {
		t.Fatal(err)
	}
	service := score.NewService(storage.NewRulesetStore(p), cache, storage.NewPurchaseStore(p))
	if err := service.UseModel(model, policy); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(service), "key")
	req := httptest.NewRequest("POST", "/score", strings.NewReader(`{"transaction_id":"old","event_time":"`+time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)+`","amount_minor":100,"player_id":"player","card_token":"card","device_id":"device","ip_country":"AZ"}`))
	req.Header.Set("X-API-Key", "key")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("score %d %s", rec.Code, rec.Body.String())
	}
	var good bool
	err = p.QueryRow(t.Context(), `SELECT model_version='lgbm-game-v9' AND model_inputs->>'hours_since_password_reset'='-1' AND model_inputs->>'device_first_login_hours'='-1' AND model_inputs->>'country_first_login_hours'='-1' AND model_inputs->>'login_devices_24h'='-1' FROM decisions`).Scan(&good)
	if err != nil || !good {
		t.Fatal("stale model inputs", good, err)
	}
}

func testUnavailableCache(t *testing.T, outage *redisOutage, owner *pgxpool.Pool, manager *simulation.Service, cache *storage.CachedBlocklistStore) {
	t.Helper()
	if _, err := owner.Exec(t.Context(), "INSERT INTO blocklist_entries(entry_type,value,reason) VALUES('card','card','stale mirror')"); err != nil {
		t.Fatal(err)
	}
	if err := cache.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	outage.offline.Store(true)
	if err := manager.Reset(t.Context(), "op"); err != nil {
		t.Fatal(err)
	}
	outage.offline.Store(false)
	if _, found, err := cache.Match(t.Context(), models.BlocklistSubject{CardToken: "card", PlayerID: "player"}); err != nil || found {
		t.Fatal("stale Redis match after reconnect", found, err)
	}
	if err := cache.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, owner)
}

// redisOutage leaves old keys intact while every attempted repair fails.
type redisOutage struct{ offline atomic.Bool }

func (h *redisOutage) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *redisOutage) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.offline.Load() {
			return errors.New("Redis unreachable")
		}
		return next(ctx, cmd)
	}
}
func (h *redisOutage) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if h.offline.Load() {
			return errors.New("Redis unreachable")
		}
		return next(ctx, cmds)
	}
}

func assertNewSimulation(t *testing.T, runtime, evaluation *pgxpool.Pool, url string) {
	t.Helper()
	mux := http.NewServeMux()
	scorer := score.NewService(storage.NewRulesetStore(runtime), storage.NewBlocklistStore(runtime), storage.NewPurchaseStore(runtime))
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(scorer), "key")
	server := httptest.NewServer(mux)
	defer server.Close()
	store := labels.NewSimulationStore(storage.NewSimulationStore(runtime, url), evaluation)
	manager, err := simulation.NewService(t.Context(), store, &simulator.Client{BaseURL: server.URL, APIKey: "key", HTTP: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	settings := simulation.DefaultSettings()
	settings.RequestID = "old-request"
	settings.Mode = "backfill"
	settings.Count = 1
	settings.Rate = 50
	run, created, err := manager.Start(t.Context(), "op", settings)
	if err != nil || !created {
		t.Fatal("start after reset", created, err)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		current, err := manager.Get(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == "completed" {
			var n int
			if err := evaluation.QueryRow(t.Context(), "SELECT count(*) FROM purchase_labels WHERE run_id=$1", run.ID).Scan(&n); err != nil || n != 1 || current.Sent != 1 {
				t.Fatal("new labels/checkpoint", n, current.Sent, err)
			}
			return
		}
		if current.Status == "failed" {
			t.Fatal("new run failed", current.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("new run did not complete")
}

func resetCache(t *testing.T, runtime *pgxpool.Pool) (*redis.Client, *storage.CachedBlocklistStore, *redisOutage) {
	t.Helper()
	r, err := testdb.StartRedis(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Stop() })
	options, err := redis.ParseURL(r.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	outage := &redisOutage{}
	client.AddHook(outage)
	cache := storage.NewCachedBlocklistStore(storage.NewBlocklistStore(runtime), client)
	if err := cache.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	return client, cache, outage
}
