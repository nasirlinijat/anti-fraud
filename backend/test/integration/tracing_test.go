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
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOneScoreRequestIsOneTraceWithItsQueries(t *testing.T) {
	ctx := context.Background()
	transactionID := "it-trace-" + time.Now().UTC().Format("150405.000000000")
	t.Cleanup(func() {
		_, _ = ownerPool.Exec(context.Background(), `DELETE FROM decisions WHERE purchase_id IN (SELECT id FROM purchases WHERE transaction_id = $1)`, transactionID)
		_, _ = ownerPool.Exec(context.Background(), `DELETE FROM purchases WHERE transaction_id = $1`, transactionID)
	})

	recorder := tracetest.NewSpanRecorder()
	platform.UseTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(platform.StopUsingTracing)

	pool, err := platform.NewPool(ctx, appDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	mux := http.NewServeMux()
	rulesets := storage.NewRulesetStore(pool)
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(score.NewService(rulesets, storage.NewBlocklistStore(pool), storage.NewPurchaseStore(pool))), serviceKey)

	req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(purchase(transactionID, 1999, "it-trace-device")))
	req.Header.Set("X-API-Key", serviceKey)
	rec := httptest.NewRecorder()
	transport.Middleware(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}

	var request sdktrace.ReadOnlySpan
	var queries []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		switch span.Name() {
		case "POST /score":
			request = span
		case "postgresql.query":
			queries = append(queries, span)
		}
	}
	if request == nil {
		t.Fatal("no span for the request")
	}
	if len(queries) < 5 {
		t.Fatalf("%d query spans, want one per database query of the scoring flow", len(queries))
	}
	sawInsert := false
	for _, q := range queries {
		if q.SpanContext().TraceID() != request.SpanContext().TraceID() {
			t.Fatalf("query span %v belongs to another trace", q.Attributes())
		}
		for _, kv := range q.Attributes() {
			value := kv.Value.Emit()
			if strings.Contains(value, "card-"+transactionID) || strings.Contains(value, "it-trace-device") {
				t.Fatalf("a query span contains a request value: %s=%s", kv.Key, value)
			}
			if kv.Key == "db.statement" && strings.HasPrefix(value, "INSERT INTO purchases") {
				sawInsert = true
			}
		}
	}
	if !sawInsert {
		t.Fatal("no span for the purchase insert")
	}
}
