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
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
	"github.com/nasirlinijat/anti-fraud/backend/test/testdb"
)

const (
	serviceKey  = "integration-service-key"
	operatorKey = "integration-operator-key"
	analystKey  = "integration-analyst-key"
	operatorID  = "integration-operator"
	analystID   = "integration-analyst"
)

var (
	ownerPool   *pgxpool.Pool
	runtimePool *pgxpool.Pool
	runtimeRole bool
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
	return m.Run()
}

type app struct {
	mux *http.ServeMux
	tx  pgx.Tx
}

func newApp(t *testing.T) app {
	t.Helper()
	ctx := context.Background()
	tx, err := runtimePool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	analysts, err := platform.ParseAnalystKeys(`[{"analyst_id":"`+analystID+`","key":"`+analystKey+`"}]`, serviceKey)
	if err != nil {
		t.Fatal(err)
	}
	operators, err := platform.ParseOperatorKeys(`[{"operator_id":"`+operatorID+`","key":"`+operatorKey+`"}]`, serviceKey, analysts)
	if err != nil {
		t.Fatal(err)
	}

	rulesets := storage.NewRulesetStore(tx)
	blocklistStore := storage.NewBlocklistStore(tx)
	mux := http.NewServeMux()
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(score.NewService(rulesets, blocklistStore, storage.NewPurchaseStore(tx))), serviceKey)
	transport.RegisterRulesetRoutes(mux, transport.NewRulesetHandler(ruleset.NewService(rulesets)), serviceKey, operators)
	transport.RegisterReviewRoutes(mux, transport.NewReviewHandler(review.NewService(storage.NewReviewStore(tx)), false), analysts)
	transport.RegisterBlocklistRoutes(mux, transport.NewBlocklistHandler(blocklist.NewService(blocklistStore)), serviceKey, operators)
	return app{mux: mux, tx: tx}
}

func (a app) do(t *testing.T, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
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
