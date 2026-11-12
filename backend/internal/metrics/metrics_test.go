package metrics

import (
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

type call struct {
	kind  string
	name  string
	value float64
	tags  []string
}

type fakeClient struct {
	calls []call
}

func (f *fakeClient) Incr(name string, tags []string, _ float64) error {
	f.calls = append(f.calls, call{"count", name, 1, tags})
	return nil
}

func (f *fakeClient) Histogram(name string, value float64, tags []string, _ float64) error {
	f.calls = append(f.calls, call{"histogram", name, value, tags})
	return nil
}

func (f *fakeClient) Close() error { return nil }

func reviewWithModel() models.Decision {
	score, version := 0.82, "lgbm-game-v5"
	return models.Decision{
		RulesetVersion: 7,
		Result: scoring.Result{Outcome: scoring.OutcomeReview, Score: 60, FiredRules: []scoring.FiredRule{
			{Name: scoring.RuleHighAmount, Points: 30},
			{Name: scoring.RuleCardVelocity, Points: 30},
		}},
		ModelScore:   &score,
		ModelVersion: &version,
		Latency:      12500 * time.Microsecond,
	}
}

func TestDecidedSendsNamesTagsAndValues(t *testing.T) {
	client := &fakeClient{}
	NewWithClient(client).Decided(reviewWithModel())

	tags := []string{"outcome:review", "ruleset_version:7", "model_version:lgbm-game-v5"}
	want := []call{
		{"count", Decisions, 1, tags},
		{"histogram", Points, 60, tags},
		{"histogram", LatencyMS, 12.5, tags},
		{"histogram", ModelScore, 0.82, tags},
		{"count", FiredRules, 1, []string{"rule:high_amount", "outcome:review", "ruleset_version:7"}},
		{"count", FiredRules, 1, []string{"rule:card_velocity", "outcome:review", "ruleset_version:7"}},
	}
	if len(client.calls) != len(want) {
		t.Fatalf("%d calls %+v, want %d", len(client.calls), client.calls, len(want))
	}
	for i, w := range want {
		got := client.calls[i]
		if got.kind != w.kind || got.name != w.name || got.value != w.value || !slices.Equal(got.tags, w.tags) {
			t.Errorf("call %d = %+v, want %+v", i, got, w)
		}
	}
}

func TestDecidedWithoutModelOrRules(t *testing.T) {
	client := &fakeClient{}
	NewWithClient(client).Decided(models.Decision{RulesetVersion: 2, Result: scoring.Result{Outcome: scoring.OutcomeAllow}})
	for _, c := range client.calls {
		if c.name == ModelScore || c.name == FiredRules {
			t.Fatalf("sent %s for a decision without a model score or fired rules", c.name)
		}
		if c.name == Decisions && !slices.Contains(c.tags, "model_version:none") {
			t.Fatalf("decision tags %v, want model_version:none", c.tags)
		}
	}
}

func TestDecidedSendsDogStatsDPackets(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	m, err := New(conn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	m.Decided(reviewWithModel())
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	var received strings.Builder
	buf := make([]byte, 65536)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		received.Write(buf[:n])
		received.WriteString("\n")
	}
	lines := received.String()
	for _, want := range []string{
		"antifraud.score.decisions:1|c|",
		"antifraud.score.points:60|h|",
		"antifraud.score.latency_ms:12.5|h|",
		"antifraud.score.model_score:0.82|h|",
		"antifraud.score.fired_rules:1|c|",
		"service:anti-fraud-backend",
		"outcome:review",
		"ruleset_version:7",
		"model_version:lgbm-game-v5",
		"rule:high_amount",
		"rule:card_velocity",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("packets do not contain %q:\n%s", want, lines)
		}
	}
}

func TestDecidedNeverWaitsForTheAgent(t *testing.T) {
	m, err := New("127.0.0.1:9")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	d := reviewWithModel()
	started := time.Now()
	for range 10000 {
		m.Decided(d)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("10,000 decisions took %v with no agent listening, want well under a second", elapsed)
	}
}
