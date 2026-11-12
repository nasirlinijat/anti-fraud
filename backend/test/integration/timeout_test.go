//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
)

func TestScoreThatRunsOutOfTimeSavesNothing(t *testing.T) {
	ctx := context.Background()
	transactionID := "it-timeout-" + time.Now().UTC().Format("150405.000000000")
	t.Cleanup(func() {
		_, _ = ownerPool.Exec(context.Background(), `DELETE FROM decisions WHERE purchase_id IN (SELECT id FROM purchases WHERE transaction_id = $1)`, transactionID)
		_, _ = ownerPool.Exec(context.Background(), `DELETE FROM purchases WHERE transaction_id = $1`, transactionID)
	})

	rulesets := storage.NewRulesetStore(runtimePool)
	mux := http.NewServeMux()
	handler := transport.NewScoreHandler(score.NewService(rulesets, storage.NewBlocklistStore(runtimePool), storage.NewPurchaseStore(runtimePool)))
	transport.RegisterScoreRoutes(mux, handler.UseLimits(platform.ScoreLimits{Timeout: 500 * time.Millisecond}), serviceKey)

	lock, err := ownerPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err := lock.Exec(ctx, `LOCK TABLE decisions IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(purchase(transactionID, 1999, "")))
	req.Header.Set("X-API-Key", serviceKey)
	rec := httptest.NewRecorder()
	started := time.Now()
	mux.ServeHTTP(rec, req)
	elapsed := time.Since(started)

	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, body %s; want 503 when the deadline passes", rec.Code, rec.Body.String())
	}
	if elapsed < 400*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("the request took %v, want it to wait for the lock and stop at the 500ms deadline", elapsed)
	}

	var purchases int
	if err := ownerPool.QueryRow(ctx, `SELECT count(*) FROM purchases WHERE transaction_id = $1`, transactionID).Scan(&purchases); err != nil {
		t.Fatal(err)
	}
	if purchases != 0 {
		t.Fatalf("%d purchases saved by a request that ran out of time, want 0", purchases)
	}

	retry := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(purchase(transactionID, 1999, "")))
	retry.Header.Set("X-API-Key", serviceKey)
	retried := httptest.NewRecorder()
	mux.ServeHTTP(retried, retry)
	if retried.Code != http.StatusOK {
		t.Fatalf("retry status %d, body %s; want 200 once the database is free", retried.Code, retried.Body.String())
	}
	if err := ownerPool.QueryRow(ctx, `SELECT count(*) FROM purchases WHERE transaction_id = $1`, transactionID).Scan(&purchases); err != nil || purchases != 1 {
		t.Fatalf("%d purchases after the retry (%v), want 1", purchases, err)
	}
}
