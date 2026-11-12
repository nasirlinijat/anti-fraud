//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	postgresImage   = "postgres:18-alpine"
	runtimePassword = "integration-runtime-password"
	labelsPassword  = "integration-labels-password"

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
		container, err := startPostgres(ctx)
		if err != nil {
			log.Printf("start postgres: %v", err)
			return 1
		}
		defer func() {
			if err := testcontainers.TerminateContainer(container); err != nil {
				log.Printf("stop postgres: %v", err)
			}
		}()
		if ownerURL, err = container.ConnectionString(ctx, "sslmode=disable"); err != nil {
			log.Printf("postgres address: %v", err)
			return 1
		}
		if runtimeURL, err = withUser(ownerURL, "antifraud_api", runtimePassword); err != nil {
			log.Printf("runtime address: %v", err)
			return 1
		}
	}

	var err error
	if ownerPool, err = pgxpool.New(ctx, ownerURL); err != nil {
		log.Printf("connect as owner: %v", err)
		return 1
	}
	defer ownerPool.Close()
	runtimePool = ownerPool
	if runtimeURL != "" {
		if runtimePool, err = pgxpool.New(ctx, runtimeURL); err != nil {
			log.Printf("connect as the runtime role: %v", err)
			return 1
		}
		defer runtimePool.Close()
		runtimeRole = true
	}
	if err := runtimePool.Ping(ctx); err != nil {
		log.Printf("ping the database: %v", err)
		return 1
	}
	return m.Run()
}

func startPostgres(ctx context.Context) (*postgres.PostgresContainer, error) {
	scripts, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil {
		return nil, err
	}
	if len(scripts) == 0 {
		return nil, fmt.Errorf("no migrations found")
	}
	slices.Sort(scripts)
	scripts = append(scripts, "../../runtime-role.sql")
	return postgres.Run(ctx, postgresImage,
		postgres.WithDatabase("antifraud"),
		postgres.WithUsername("antifraud"),
		postgres.WithPassword("antifraud"),
		postgres.WithInitScripts(scripts...),
		testcontainers.WithEnv(map[string]string{
			"RUNTIME_DB_PASSWORD": runtimePassword,
			"LABELS_DB_PASSWORD":  labelsPassword,
		}),
		postgres.BasicWaitStrategies(),
	)
}

func withUser(dsn, user, password string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(user, password)
	return u.String(), nil
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
