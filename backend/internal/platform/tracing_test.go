package platform

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	UseTracerProvider(newTracerProvider(sdktrace.WithSpanProcessor(recorder), 1))
	t.Cleanup(StopUsingTracing)
	return recorder
}

func TestTracingIsOffWithoutAnEndpoint(t *testing.T) {
	stop, err := StartTracing(context.Background(), "", 1)
	if err != nil || TracingEnabled() {
		t.Fatalf("StartTracing without an endpoint: enabled %v, err %v", TracingEnabled(), err)
	}
	if err := stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestQuerySpanHasTheStatementButNoValues(t *testing.T) {
	recorder := recordSpans(t)
	ctx := queryTracer{}.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL:  "SELECT  count(*)\n\tFROM purchases\n WHERE card_token = $1",
		Args: []any{"card-token-secret-4111"},
	})
	queryTracer{}.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "postgresql.query" {
		t.Fatalf("spans %v, want one postgresql.query", spans)
	}
	attrs := map[string]string{}
	for _, kv := range spans[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	if attrs["db.statement"] != "SELECT count(*) FROM purchases WHERE card_token = $1" || attrs["db.operation"] != "SELECT" || attrs["db.system"] != "postgresql" {
		t.Fatalf("attributes %v", attrs)
	}
	for key, value := range attrs {
		if strings.Contains(value, "card-token-secret") {
			t.Fatalf("attribute %s contains a query value: %s", key, value)
		}
	}
}

func TestQuerySpanMarksFailuresButNotMissingRows(t *testing.T) {
	recorder := recordSpans(t)
	for _, err := range []error{errors.New("connection lost"), pgx.ErrNoRows} {
		ctx := queryTracer{}.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
		queryTracer{}.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: err})
	}
	spans := recorder.Ended()
	if spans[0].Status().Code != codes.Error || spans[1].Status().Code == codes.Error {
		t.Fatalf("statuses %v and %v, want an error only for the failed query", spans[0].Status(), spans[1].Status())
	}
}

func TestLogLinesCarryTheDatadogTraceID(t *testing.T) {
	recordSpans(t)
	ctx, span := otel.Tracer("test").Start(context.Background(), "request")
	defer span.End()

	var buf bytes.Buffer
	NewLogger(&buf).InfoContext(ctx, "scored")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	traceID := span.SpanContext().TraceID()
	want := strconv.FormatUint(binary.BigEndian.Uint64(traceID[8:]), 10)
	if line["dd.trace_id"] != want || line["dd.span_id"] == nil {
		t.Fatalf("log line %v, want dd.trace_id %s", line, want)
	}

	buf.Reset()
	NewLogger(&buf).Info("no request")
	if strings.Contains(buf.String(), "dd.trace_id") {
		t.Fatalf("a line without a span has a trace ID: %s", buf.String())
	}
}

func TestTracingSampleRatio(t *testing.T) {
	requiredConfig(t)
	t.Setenv("TRACING_ENDPOINT", " datadog:4317 ")
	t.Setenv("TRACING_SAMPLE_RATIO", "0.25")
	cfg, err := LoadConfig()
	if err != nil || cfg.Tracing != (TracingConfig{Endpoint: "datadog:4317", SampleRatio: 0.25}) {
		t.Fatalf("Tracing = %+v, %v", cfg.Tracing, err)
	}
	for _, bad := range []string{"-0.1", "1.5", "NaN", "half"} {
		t.Setenv("TRACING_SAMPLE_RATIO", bad)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("TRACING_SAMPLE_RATIO=%s was accepted", bad)
		}
	}
	t.Setenv("TRACING_ENDPOINT", "")
	t.Setenv("TRACING_SAMPLE_RATIO", "")
	if cfg, err := LoadConfig(); err != nil || cfg.Tracing != (TracingConfig{SampleRatio: 1}) {
		t.Fatalf("defaults = %+v, %v", cfg.Tracing, err)
	}
}

type discardExporter struct{}

func (discardExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (discardExporter) Shutdown(context.Context) error                             { return nil }

func BenchmarkQuerySpan(b *testing.B) {
	UseTracerProvider(newTracerProvider(sdktrace.WithBatcher(discardExporter{}), 1))
	b.Cleanup(StopUsingTracing)
	data := pgx.TraceQueryStartData{SQL: "SELECT count(*) FROM purchases WHERE card_token = $1 AND event_time > $2"}
	for range b.N {
		ctx := queryTracer{}.TraceQueryStart(context.Background(), nil, data)
		queryTracer{}.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	}
}
