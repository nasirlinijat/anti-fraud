package platform

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var tracingEnabled atomic.Bool

func TracingEnabled() bool {
	return tracingEnabled.Load()
}

func StartTracing(ctx context.Context, endpoint string, sampleRatio float64) (func(context.Context) error, error) {
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	provider := newTracerProvider(sdktrace.WithBatcher(exporter), sampleRatio)
	UseTracerProvider(provider)
	return provider.Shutdown, nil
}

func newTracerProvider(processor sdktrace.TracerProviderOption, sampleRatio float64) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		processor,
		sdktrace.WithResource(resource.NewSchemaless(semconv.ServiceName(ServiceName))),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRatio))),
	)
}

func UseTracerProvider(provider trace.TracerProvider) {
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tracingEnabled.Store(true)
}

func StopUsingTracing() {
	otel.SetTracerProvider(noop.NewTracerProvider())
	tracingEnabled.Store(false)
}

func datadogID(id []byte) string {
	return strconv.FormatUint(binary.BigEndian.Uint64(id[len(id)-8:]), 10)
}

func traceAttrs(ctx context.Context) []any {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}
	traceID, spanID := sc.TraceID(), sc.SpanID()
	return []any{"dd.trace_id", datadogID(traceID[:]), "dd.span_id", datadogID(spanID[:])}
}

type queryTracer struct{}

func (queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, span := otel.Tracer("anti-fraud/postgres").Start(ctx, "postgresql.query", trace.WithSpanKind(trace.SpanKindClient))
	if span.IsRecording() {
		statement := strings.Join(strings.Fields(data.SQL), " ")
		operation, _, _ := strings.Cut(statement, " ")
		span.SetAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", strings.ToUpper(operation)),
			attribute.String("db.statement", statement),
		)
	}
	return ctx
}

func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, "query failed")
	}
	span.End()
}
