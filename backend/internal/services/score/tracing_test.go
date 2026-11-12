package score_test

import (
	"context"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"
)

func TestScoreRecordsModelAndRulesSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	platform.UseTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(platform.StopUsingTracing)

	d := newScoreDeps(t)
	d.useModel(t, scoring.ModelPolicy{ClearBelow: 0.05, FlagAt: 0.6})
	p := purchaseOf(20_000)
	d.expectUnsaved(p, rulesetWith(scoring.ModelAssist), 0)
	d.model.EXPECT().Predict(gomock.Any()).Return(0.3, nil)
	d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).Return(int64(1), nil)

	ctx, request := otel.Tracer("test").Start(context.Background(), "POST /score")
	if _, err := d.service.Score(ctx, p, time.Now()); err != nil {
		t.Fatal(err)
	}
	request.End()

	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range recorder.Ended() {
		byName[span.Name()] = span
	}
	model, rules := byName["score.model"], byName["score.rules"]
	if model == nil || rules == nil {
		t.Fatalf("spans %v, want score.model and score.rules", byName)
	}
	parent := byName["POST /score"].SpanContext().SpanID()
	if model.Parent().SpanID() != parent || rules.Parent().SpanID() != parent {
		t.Fatal("the model and rules spans are not children of the request span")
	}
	attrs := func(s sdktrace.ReadOnlySpan) map[string]string {
		m := map[string]string{}
		for _, kv := range s.Attributes() {
			m[string(kv.Key)] = kv.Value.String()
		}
		return m
	}
	if got := attrs(model); got["model.version"] != mockModelVersion || got["model.score"] != "0.3" {
		t.Fatalf("model span attributes %v", got)
	}
	if got := attrs(rules); got["score.outcome"] != "review" || got["score.points"] != "60" {
		t.Fatalf("rules span attributes %v", got)
	}
}
