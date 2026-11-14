//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/account"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/accountevent"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/auth"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
	"github.com/nasirlinijat/anti-fraud/backend/test/testdb"
	"github.com/redis/go-redis/v9"
)

const (
	serviceKey    = "integration-service-key"
	adminToken    = "integration-admin-session"
	reviewerToken = "integration-reviewer-session"
	operatorID    = "integration-operator"
	analystID     = "integration-analyst"
)

var (
	ownerPool         *pgxpool.Pool
	runtimePool       *pgxpool.Pool
	runtimeRole       bool
	testRedisInstance *testdb.Redis
	testRedisClient   *redis.Client
	appDatabaseURL    string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	ownerURL := os.Getenv("TEST_DATABASE_URL")
	runtimeURL := os.Getenv("TEST_RUNTIME_DATABASE_URL")
	if ownerURL == "" {
		db, err := testdb.Start(ctx)
		if err != nil {
			slog.Error("start postgres", "error", err)
			return 1
		}
		defer func() {
			if err := db.Stop(); err != nil {
				slog.Error("stop postgres", "error", err)
			}
		}()
		ownerURL, runtimeURL = db.OwnerURL, db.RuntimeURL
	}

	appDatabaseURL = ownerURL
	if runtimeURL != "" {
		appDatabaseURL = runtimeURL
	}

	var err error
	if ownerPool, err = pgxpool.New(ctx, ownerURL); err != nil {
		slog.Error("connect as owner", "error", err)
		return 1
	}
	defer ownerPool.Close()
	runtimePool = ownerPool
	if runtimeURL != "" {
		if runtimePool, err = pgxpool.New(ctx, runtimeURL); err != nil {
			slog.Error("connect as the runtime role", "error", err)
			return 1
		}
		defer runtimePool.Close()
		runtimeRole = true
	}
	if err := runtimePool.Ping(ctx); err != nil {
		slog.Error("ping the database", "error", err)
		return 1
	}

	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		r, err := testdb.StartRedis(ctx)
		if err != nil {
			slog.Error("start redis", "error", err)
			return 1
		}
		defer func() {
			if err := r.Stop(); err != nil {
				slog.Error("stop redis", "error", err)
			}
		}()
		testRedisInstance = r
		redisURL = r.URL
	}
	rClient, err := platform.NewRedis(redisURL)
	if err != nil {
		slog.Error("connect to redis", "error", err)
		return 1
	}
	defer rClient.Close()
	testRedisClient = rClient

	return m.Run()
}

type app struct {
	mux      *http.ServeMux
	tx       pgx.Tx
	accounts *account.Service
	sessions *auth.Service
}

func newApp(t *testing.T) app {
	t.Helper()
	ctx := context.Background()
	tx, err := runtimePool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	rulesets := storage.NewRulesetStore(tx)
	rawBlocklist := storage.NewBlocklistStore(tx)
	var blocklistSvc score.Blocklist = rawBlocklist
	var blocklistStore blocklist.Store = rawBlocklist
	reviewStore := storage.NewReviewStore(tx)

	if testRedisClient != nil {
		cachedBlocklist := storage.NewCachedBlocklistStore(rawBlocklist, testRedisClient)
		if err := cachedBlocklist.Reload(ctx); err != nil {
			t.Fatalf("reload blocklist cache: %v", err)
		}
		t.Cleanup(func() {
			_ = testRedisClient.FlushDB(context.Background()).Err()
		})
		reviewStore.UseBlocklistCache(cachedBlocklist)
		blocklistSvc = cachedBlocklist
		blocklistStore = cachedBlocklist
	}

	mux := http.NewServeMux()
	sessions := auth.NewService(storage.NewAccountStore(tx), storage.NewSessionStore(tx))
	access := platform.Access{Sessions: sessions}
	accountEvents := storage.NewAccountEventStore(tx)
	scoreService := score.NewService(rulesets, blocklistSvc, storage.NewPurchaseStore(tx)).UseAccountEvents(accountEvents)
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(scoreService), serviceKey)
	transport.RegisterAccountEventRoutes(mux, transport.NewAccountEventHandler(accountevent.NewService(accountEvents)), serviceKey)
	transport.RegisterRulesetRoutes(mux, transport.NewRulesetHandler(ruleset.NewService(rulesets)), access)
	transport.RegisterReviewRoutes(mux, transport.NewReviewHandler(review.NewService(reviewStore), false), access)
	transport.RegisterBlocklistRoutes(mux, transport.NewBlocklistHandler(blocklist.NewService(blocklistStore)), access)
	return app{mux: mux, tx: tx, accounts: account.NewService(storage.NewAccountStore(tx)), sessions: sessions}
}

func (a app) do(t *testing.T, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	if !strings.HasPrefix(path, "/score") && !strings.HasPrefix(path, "/account-events") && (key == adminToken || key == reviewerToken) {
		role := models.RoleReviewer
		username := analystID
		if key == adminToken {
			role = models.RoleAdmin
			username = "integration-admin"
		}
		var id int64
		err := a.tx.QueryRow(context.Background(), "SELECT id FROM accounts WHERE username=$1", username).Scan(&id)
		if err != nil {
			if _, err = a.accounts.Create(context.Background(), account.NewAccount{Username: username, Password: "integration password", Role: role}); err != nil {
				t.Fatal(err)
			}
		}
		session, err := a.sessions.Login(context.Background(), username, "integration password")
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost && strings.HasSuffix(path, "/decision") {
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/reviews/"), "/decision")
			if _, err := a.tx.Exec(context.Background(), `INSERT INTO review_claims(purchase_id,account_id) SELECT d.purchase_id,a.id FROM decisions d JOIN accounts a ON a.username=$2 WHERE d.purchase_id=$1::bigint AND d.outcome='review' ON CONFLICT(purchase_id) DO NOTHING`, id, username); err != nil {
				t.Fatal(err)
			}
		}
		req.Header.Del("X-API-Key")
		req.Header.Set("Authorization", "Bearer "+session.Token)
	}
	a.mux.ServeHTTP(rec, req)
	return rec
}

// signIn creates a reviewer account and returns a session token for it.
func (a app) signIn(t *testing.T, username string) string {
	t.Helper()
	const password = "integration test password"
	ctx := context.Background()
	if _, err := a.accounts.Create(ctx, account.NewAccount{Username: username, Password: password, Role: models.RoleReviewer}); err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	session, err := a.sessions.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("sign in %s: %v", username, err)
	}
	return session.Token
}

// doAs sends a request with the session token instead of a key.
func (a app) doAs(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	return rec
}

func expect[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

type scoreResponse struct {
	TransactionID  string `json:"transaction_id"`
	Outcome        string `json:"outcome"`
	Score          int    `json:"score"`
	RulesetVersion int    `json:"ruleset_version"`
	FiredRules     []struct {
		Name string `json:"name"`
	} `json:"fired_rules"`
}

func purchase(transactionID string, amountMinor int64, deviceID string) string {
	device := ""
	if deviceID != "" {
		device = fmt.Sprintf(`,"device_id":%q`, deviceID)
	}
	eventTime := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	return fmt.Sprintf(`{"transaction_id":%q,"event_time":%q,"amount_minor":%d,"player_id":"player-%s","card_token":"card-%s"%s}`,
		transactionID, eventTime, amountMinor, transactionID, transactionID, device)
}

const highAmountRuleset = `{
	"review_threshold": 50,
	"block_threshold": 80,
	"note": "integration test",
	"rules": {
		"high_amount": {"enabled": true, "weight": 60, "limit": 10000},
		"card_velocity": {"enabled": true, "weight": 30, "limit": 5}
	}
}`
