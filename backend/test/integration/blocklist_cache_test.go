//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
	"github.com/nasirlinijat/anti-fraud/backend/test/testdb"
)

type blocklistAddResponse struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

func purchaseWithCard(transactionID, cardToken string, amountMinor int64) string {
	eventTime := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	return fmt.Sprintf(`{"transaction_id":%q,"event_time":%q,"amount_minor":%d,"player_id":"player-%s","card_token":%q}`,
		transactionID, eventTime, amountMinor, transactionID, cardToken)
}

func TestBlocklistCacheAddAndRemoveViaHTTP(t *testing.T) {
	a := newApp(t)

	// Add entry via operator endpoint.
	addBody := `{"type":"card","value":"cached-card-99","reason":"test card fraud"}`
	saved := expect[blocklistAddResponse](t, a.do(t, http.MethodPost, "/blocklist", operatorKey, addBody), http.StatusCreated)
	if saved.ID <= 0 || saved.Value != "cached-card-99" {
		t.Fatalf("saved blocklist entry = %+v", saved)
	}

	// Verify key in Redis.
	if testRedisClient != nil {
		raw := testRedisClient.HGet(context.Background(), "antifraud:blocklist:card", "cached-card-99").Val()
		if raw == "" {
			t.Error("entry not found in Redis antifraud:blocklist:card")
		}
	}

	// Score with this card -> blocked.
	blocked := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchaseWithCard("tx-cached-block", "cached-card-99", 1000)), http.StatusOK)
	if blocked.Outcome != "block" || len(blocked.FiredRules) != 1 || blocked.FiredRules[0].Name != "blocklist" {
		t.Fatalf("score response = %+v, want blocklist block", blocked)
	}

	// Remove entry via operator endpoint.
	deletePath := fmt.Sprintf("/blocklist/%d", saved.ID)
	rec := a.do(t, http.MethodDelete, deletePath, operatorKey, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rec.Code)
	}

	// Verify deleted from Redis.
	if testRedisClient != nil {
		raw := testRedisClient.HGet(context.Background(), "antifraud:blocklist:card", "cached-card-99").Val()
		if raw != "" {
			t.Errorf("entry still in Redis after delete: %s", raw)
		}
	}

	// Score again with the removed card -> allowed.
	allowed := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchaseWithCard("tx-cached-allow", "cached-card-99", 1000)), http.StatusOK)
	if allowed.Outcome != "allow" {
		t.Fatalf("score response = %+v, want allow", allowed)
	}
}

func TestBlocklistCacheFallbackWhenRedisStopped(t *testing.T) {
	ctx := context.Background()
	disposableRedis, err := testdb.StartRedis(ctx)
	if err != nil {
		t.Fatalf("start disposable redis: %v", err)
	}
	defer disposableRedis.Stop()

	client, err := platform.NewRedis(disposableRedis.URL)
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	defer client.Close()

	tx, err := runtimePool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(context.Background())

	analysts, err := platform.ParseAnalystKeys(`[{"analyst_id":"`+analystID+`","key":"`+analystKey+`"}]`, serviceKey)
	if err != nil {
		t.Fatal(err)
	}
	operators, err := platform.ParseOperatorKeys(`[{"operator_id":"`+operatorID+`","key":"`+operatorKey+`"}]`, serviceKey, analysts)
	if err != nil {
		t.Fatal(err)
	}

	rulesets := storage.NewRulesetStore(tx)
	rawBlocklist := storage.NewBlocklistStore(tx)
	cachedBlocklist := storage.NewCachedBlocklistStore(rawBlocklist, client)
	if err := cachedBlocklist.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}

	reviewStore := storage.NewReviewStore(tx).UseBlocklistCache(cachedBlocklist)
	mux := http.NewServeMux()
	access := platform.Access{ServiceKey: serviceKey, Analysts: analysts, Operators: operators}
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(score.NewService(rulesets, cachedBlocklist, storage.NewPurchaseStore(tx))), serviceKey)
	transport.RegisterRulesetRoutes(mux, transport.NewRulesetHandler(ruleset.NewService(rulesets)), access)
	transport.RegisterReviewRoutes(mux, transport.NewReviewHandler(review.NewService(reviewStore), false), access)
	transport.RegisterBlocklistRoutes(mux, transport.NewBlocklistHandler(blocklist.NewService(cachedBlocklist)), access)
	a := app{mux: mux, tx: tx}

	// Add entry
	addBody := `{"type":"card","value":"card-redis-down","reason":"test fallback"}`
	expect[blocklistAddResponse](t, a.do(t, http.MethodPost, "/blocklist", operatorKey, addBody), http.StatusCreated)

	// Stop container
	if err := disposableRedis.StopContainer(ctx); err != nil {
		t.Fatalf("stop redis container: %v", err)
	}

	// Score while stopped -> falls back to DB and blocks!
	res := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchaseWithCard("tx-redis-down", "card-redis-down", 1000)), http.StatusOK)
	if res.Outcome != "block" || len(res.FiredRules) != 1 || res.FiredRules[0].Name != "blocklist" {
		t.Fatalf("score on down redis = %+v, want blocklist block via fallback", res)
	}
}
