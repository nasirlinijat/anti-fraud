//go:build integration

package concurrency

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
	"github.com/nasirlinijat/anti-fraud/backend/test/testdb"
)

const (
	serviceKey     = "concurrency-service-key"
	operatorKey    = "concurrency-operator-key"
	firstAnalyst   = "concurrency-analyst-1"
	secondAnalyst  = "concurrency-analyst-2"
	operatorID     = "concurrency-operator"
	highAmountRule = `{
	"review_threshold": 50,
	"block_threshold": 80,
	"note": "concurrency test",
	"rules": {"high_amount": {"enabled": true, "weight": 60, "limit": 10000}}
}`
)

var (
	ownerPool *pgxpool.Pool
	server    *httptest.Server
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	db, err := testdb.Start(ctx)
	if err != nil {
		log.Printf("start postgres: %v", err)
		return 1
	}
	defer func() {
		if err := db.Stop(); err != nil {
			log.Printf("stop postgres: %v", err)
		}
	}()
	if ownerPool, err = pgxpool.New(ctx, db.OwnerURL); err != nil {
		log.Printf("connect as owner: %v", err)
		return 1
	}
	defer ownerPool.Close()
	runtimePool, err := pgxpool.New(ctx, db.RuntimeURL)
	if err != nil {
		log.Printf("connect as the runtime role: %v", err)
		return 1
	}
	defer runtimePool.Close()

	mux, err := newMux(runtimePool)
	if err != nil {
		log.Printf("wire the API: %v", err)
		return 1
	}
	server = httptest.NewServer(mux)
	defer server.Close()
	return m.Run()
}

func newMux(pool *pgxpool.Pool) (*http.ServeMux, error) {
	analysts, err := platform.ParseAnalystKeys(`[{"analyst_id":"analyst-1","key":"`+firstAnalyst+`"},{"analyst_id":"analyst-2","key":"`+secondAnalyst+`"}]`, serviceKey)
	if err != nil {
		return nil, err
	}
	operators, err := platform.ParseOperatorKeys(`[{"operator_id":"`+operatorID+`","key":"`+operatorKey+`"}]`, serviceKey, analysts)
	if err != nil {
		return nil, err
	}
	rulesets := storage.NewRulesetStore(pool)
	mux := http.NewServeMux()
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(score.NewService(rulesets, storage.NewBlocklistStore(pool), storage.NewPurchaseStore(pool))), serviceKey)
	transport.RegisterRulesetRoutes(mux, transport.NewRulesetHandler(ruleset.NewService(rulesets)), serviceKey, operators)
	transport.RegisterReviewRoutes(mux, transport.NewReviewHandler(review.NewService(storage.NewReviewStore(pool)), false), analysts)
	return mux, nil
}

type response struct {
	status int
	body   string
}

func send(method, path, key, body string) (response, error) {
	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		return response{}, err
	}
	req.Header.Set("X-API-Key", key)
	resp, err := server.Client().Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: string(b)}, err
}

func sendAll(t *testing.T, n int, request func(i int) (method, path, key, body string)) []response {
	t.Helper()
	responses := make([]response, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			method, path, key, body := request(i)
			<-start
			responses[i], errs[i] = send(method, path, key, body)
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	return responses
}

func decode[T any](t *testing.T, r response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
	return v
}

func purchase(transactionID string, amountMinor int64) string {
	eventTime := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	return fmt.Sprintf(`{"transaction_id":%q,"event_time":%q,"amount_minor":%d,"player_id":"player-%s","card_token":"card-%s"}`,
		transactionID, eventTime, amountMinor, transactionID, transactionID)
}

func unique(name string) string {
	return fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := ownerPool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
