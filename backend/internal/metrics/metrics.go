package metrics

import (
	"strconv"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

const (
	Namespace   = "antifraud."
	Decisions   = "score.decisions"
	FiredRules  = "score.fired_rules"
	Points      = "score.points"
	ModelScore  = "score.model_score"
	LatencyMS   = "score.latency_ms"
	noModelName = "none"
)

type Client interface {
	Incr(name string, tags []string, rate float64) error
	Histogram(name string, value float64, tags []string, rate float64) error
	Close() error
}

type DecisionMetrics struct {
	client Client
}

func New(addr string) (*DecisionMetrics, error) {
	client, err := statsd.New(addr,
		statsd.WithNamespace(Namespace),
		statsd.WithTags([]string{"service:" + platform.ServiceName}),
		statsd.WithoutTelemetry(),
		statsd.WithChannelMode(),
		statsd.WithoutChannelModeErrorsWhenFull(),
	)
	if err != nil {
		return nil, err
	}
	return &DecisionMetrics{client: client}, nil
}

func NewWithClient(client Client) *DecisionMetrics {
	return &DecisionMetrics{client: client}
}

func (m *DecisionMetrics) Decided(d models.Decision) {
	outcome := "outcome:" + string(d.Result.Outcome)
	model := noModelName
	if d.ModelVersion != nil {
		model = *d.ModelVersion
	}
	tags := []string{outcome, "ruleset_version:" + strconv.Itoa(d.RulesetVersion), "model_version:" + model}

	_ = m.client.Incr(Decisions, tags, 1)
	_ = m.client.Histogram(Points, float64(d.Result.Score), tags, 1)
	_ = m.client.Histogram(LatencyMS, float64(d.Latency.Microseconds())/1000, tags, 1)
	if d.ModelScore != nil {
		_ = m.client.Histogram(ModelScore, *d.ModelScore, tags, 1)
	}
	for _, rule := range d.Result.FiredRules {
		_ = m.client.Incr(FiredRules, []string{"rule:" + string(rule.Name), outcome, "ruleset_version:" + strconv.Itoa(d.RulesetVersion)}, 1)
	}
}

func (m *DecisionMetrics) Close() error {
	return m.client.Close()
}
