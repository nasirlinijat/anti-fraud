package transport

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fixedScore struct{}

func (fixedScore) Score(context.Context, models.Purchase, time.Time) (models.Decision, error) {
	return models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}, nil
}

func tracedScoreHandler(t testing.TB, recorder sdktrace.SpanProcessor) http.Handler {
	if recorder != nil {
		platform.UseTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
		t.Cleanup(platform.StopUsingTracing)
	}
	mux := http.NewServeMux()
	platform.RegisterHealthRoutes(mux, nil)
	RegisterScoreRoutes(mux, NewScoreHandler(fixedScore{}), mockServiceKey)
	return Middleware(mux)
}

func attributesOf(kvs []attribute.KeyValue) map[string]string {
	attrs := map[string]string{}
	for _, kv := range kvs {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	return attrs
}

func TestRequestSpanHasTheRouteAndTheRequestID(t *testing.T) {
	buf := captureLogs(t)
	recorder := tracetest.NewSpanRecorder()
	handler := tracedScoreHandler(t, recorder)

	rec := send(handler, http.MethodPost, "/score", mockServiceKey, minimalBody)
	wantResponse(t, rec, http.StatusOK)
	send(handler, http.MethodGet, "/health", "", "")

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("%d spans, want one for /score and none for /health", len(spans))
	}
	span := spans[0]
	attrs := attributesOf(span.Attributes())
	if span.Name() != "POST /score" || attrs["http.route"] != "/score" || attrs["request_id"] != rec.Header().Get("X-Request-ID") || attrs["http.response.status_code"] != "200" {
		t.Fatalf("span %q with %v", span.Name(), attrs)
	}

	traceID := span.SpanContext().TraceID()
	want := strconv.FormatUint(binary.BigEndian.Uint64(traceID[8:]), 10)
	request := findLine(t, logLines(t, buf), "http request")
	if request["dd.trace_id"] != want {
		t.Fatalf("request log dd.trace_id %v, want %s, so Datadog can link the log to the trace", request["dd.trace_id"], want)
	}
}

func TestNoSpansWhenTracingIsOff(t *testing.T) {
	buf := captureLogs(t)
	rec := send(tracedScoreHandler(t, nil), http.MethodPost, "/score", mockServiceKey, minimalBody)
	wantResponse(t, rec, http.StatusOK)
	if strings.Contains(buf.String(), "dd.trace_id") {
		t.Fatalf("trace IDs in the log while tracing is off: %s", buf.String())
	}
}

type discardExporter struct{}

func (discardExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (discardExporter) Shutdown(context.Context) error                             { return nil }

func BenchmarkScoreRequest(b *testing.B) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name      string
		processor sdktrace.SpanProcessor
	}{
		{"tracing off", nil},
		{"tracing on", sdktrace.NewBatchSpanProcessor(discardExporter{})},
	} {
		b.Run(tc.name, func(b *testing.B) {
			handler := tracedScoreHandler(b, tc.processor)
			b.ResetTimer()
			for range b.N {
				req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(minimalBody))
				req.Header.Set("X-API-Key", mockServiceKey)
				handler.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
