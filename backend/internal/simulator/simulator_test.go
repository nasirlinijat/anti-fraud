package simulator_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	scoresvc "github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
)

const testKey = "test-key"

// defaultRuleset matches ruleset version 1 from migration 000002.
var defaultRuleset = scoring.Ruleset{
	Version:         1,
	ReviewThreshold: 50,
	BlockThreshold:  80,
	Rules: map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleHighAmount:      {Enabled: true, Weight: 30, Limit: 50000},
		scoring.RuleCardVelocity:    {Enabled: true, Weight: 40, Limit: 5},
		scoring.RuleNewAccount:      {Enabled: true, Weight: 20, Limit: 24},
		scoring.RuleCountryMismatch: {Enabled: true, Weight: 25},
	},
	Model: scoring.ModelSettings{Mode: scoring.ModelAssist},
}

type rulesets struct{}

func (rulesets) Active(context.Context) (scoring.Ruleset, error) { return defaultRuleset, nil }

type noBlocklist struct{}

func (noBlocklist) Match(context.Context, models.BlocklistSubject) (models.BlocklistEntry, bool, error) {
	return models.BlocklistEntry{}, false, nil
}

// purchases keeps saved purchases in memory and counts card uses like
// CountCardPurchasesLastHour.
type purchases struct {
	mu        sync.Mutex
	saved     []models.Purchase
	decisions []models.Decision
	ids       map[string]int
}

func (s *purchases) FindSavedDecision(_ context.Context, p models.Purchase) (*models.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.ids[p.TransactionID]
	if !ok {
		return nil, nil
	}
	if !reflect.DeepEqual(s.saved[i], p) {
		return nil, models.ErrDuplicateTransaction
	}
	decision := s.decisions[i]
	return &decision, nil
}

func (s *purchases) CardPurchasesLastHour(_ context.Context, card string, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.saved {
		if p.CardToken == card && p.EventTime.After(at.Add(-time.Hour)) && !p.EventTime.After(at) {
			n++
		}
	}
	return n, nil
}

func (s *purchases) FeatureHistory(context.Context, features.Input) (features.History, error) {
	return features.History{}, nil
}

func (s *purchases) DeviceCardsLastHour(context.Context, string, string, time.Time) (int64, error) {
	return 0, errors.New("unexpected device history lookup")
}

func (s *purchases) TrustedHistory(context.Context, models.Purchase, int32) (*scoring.TrustedPurchaseHistory, error) {
	return nil, errors.New("unexpected trusted history lookup")
}

func (s *purchases) PlayerEarlierModelScores(context.Context, string, time.Time, string) (float64, int64, error) {
	return 0, 0, nil
}

func (s *purchases) Save(_ context.Context, p models.Purchase, d models.Decision) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[p.TransactionID]; ok {
		return 0, models.ErrDuplicateTransaction
	}
	s.ids[p.TransactionID] = len(s.saved)
	s.saved = append(s.saved, p)
	s.decisions = append(s.decisions, d)
	return int64(len(s.saved)), nil
}

func newServer(t *testing.T) (*httptest.Server, *purchases) {
	t.Helper()
	store := &purchases{ids: map[string]int{}}
	mux := http.NewServeMux()
	transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(scoresvc.NewService(rulesets{}, noBlocklist{}, store)), testKey)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

func generate(t *testing.T, cfg simulator.Config, count int) []simulator.Purchase {
	t.Helper()
	got, err := simulator.Generate(cfg, count)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return got
}

func TestGenerateIsDeterministic(t *testing.T) {
	cfg := simulator.DefaultConfig()
	first := generate(t, cfg, 400)
	second := generate(t, cfg, 400)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("the same config gave different purchases")
	}

	cfg.Seed = 2
	other := generate(t, cfg, 400)
	if reflect.DeepEqual(first, other) {
		t.Fatal("a different seed gave the same purchases")
	}
}

func TestGenerateIsAPrefixOfLongerRuns(t *testing.T) {
	cfg := simulator.DefaultConfig()
	short := generate(t, cfg, 150)
	long := generate(t, cfg, 600)
	if !reflect.DeepEqual(short, long[:150]) {
		t.Fatal("the first 150 purchases differ between a run of 150 and a run of 600")
	}
}

func TestGenerateOrderAndIDs(t *testing.T) {
	cfg := simulator.DefaultConfig()
	got := generate(t, cfg, 1000)
	if len(got) != 1000 {
		t.Fatalf("got %d purchases, want 1000", len(got))
	}
	seen := map[string]bool{}
	for i, p := range got {
		r := p.Request
		if seen[r.TransactionID] {
			t.Fatalf("transaction ID %s is repeated", r.TransactionID)
		}
		seen[r.TransactionID] = true
		if i > 0 && r.EventTime.Before(got[i-1].Request.EventTime) {
			t.Fatalf("purchase %d is earlier than purchase %d", i, i-1)
		}
		if r.EventTime.Before(cfg.Start) {
			t.Fatalf("purchase %d at %v is before the start %v", i, r.EventTime, cfg.Start)
		}
		if r.AccountCreatedAt != nil && r.AccountCreatedAt.After(r.EventTime) {
			t.Fatalf("purchase %d has account_created_at %v after its event time", i, r.AccountCreatedAt)
		}
	}
}

func TestStartEndingAtEndsTheTrafficAtTheGivenTime(t *testing.T) {
	cfg := simulator.DefaultConfig()
	end := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	start, err := simulator.StartEndingAt(cfg, 500, end)
	if err != nil {
		t.Fatalf("StartEndingAt: %v", err)
	}
	if !start.Before(end) {
		t.Fatalf("start %v is not before the end %v", start, end)
	}
	shifted := cfg
	shifted.Start = start
	got := generate(t, shifted, 500)
	if last := got[len(got)-1].Request.EventTime; !last.Equal(end) {
		t.Fatalf("last purchase at %v, want %v", last, end)
	}
	original := generate(t, cfg, 500)
	for i := range got {
		if got[i].Request.TransactionID != original[i].Request.TransactionID ||
			got[i].Request.EventTime.Sub(start) != original[i].Request.EventTime.Sub(cfg.Start) {
			t.Fatalf("purchase %d differs from the run that starts at %v", i, cfg.Start)
		}
	}
}

func TestGenerateHonoursSuspiciousShare(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0
	for _, p := range generate(t, cfg, 300) {
		if p.Pattern != simulator.PatternNormal {
			t.Fatalf("got pattern %s with suspicious share 0", p.Pattern)
		}
	}

	cfg.SuspiciousShare = 1
	seen := make(map[simulator.Pattern]bool)
	for _, p := range generate(t, cfg, 300) {
		if p.Pattern == simulator.PatternNormal {
			t.Fatal("got a normal purchase with suspicious share 1")
		}
		seen[p.Pattern] = true
	}
	for _, pattern := range simulator.Patterns[1:] {
		if !seen[pattern] {
			t.Errorf("missing special scenario %s with suspicious share 1", pattern)
		}
	}
}

func TestGenerateRejectsBadConfig(t *testing.T) {
	bad := map[string]func(*simulator.Config){
		"empty run":        func(c *simulator.Config) { c.Run = "" },
		"no start":         func(c *simulator.Config) { c.Start = time.Time{} },
		"share above 1":    func(c *simulator.Config) { c.SuspiciousShare = 1.5 },
		"negative share":   func(c *simulator.Config) { c.SuspiciousShare = -0.1 },
		"no players":       func(c *simulator.Config) { c.Players = 0 },
		"zero mean gap":    func(c *simulator.Config) { c.MeanGap = 0 },
		"negative gap":     func(c *simulator.Config) { c.MeanGap = -time.Second },
		"missing above 1":  func(c *simulator.Config) { c.MissingFieldShare = 1.5 },
		"negative missing": func(c *simulator.Config) { c.MissingFieldShare = -0.1 },
	}
	for name, change := range bad {
		t.Run(name, func(t *testing.T) {
			cfg := simulator.DefaultConfig()
			change(&cfg)
			if _, err := simulator.Generate(cfg, 10); err == nil {
				t.Fatal("Generate accepted a bad config")
			}
		})
	}
	if _, err := simulator.Generate(simulator.DefaultConfig(), -1); err == nil {
		t.Fatal("Generate accepted a negative count")
	}
}

func TestMixedRunReportsActualRulesOnlyOutcomes(t *testing.T) {
	srv, store := newServer(t)
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.2
	// The expected rule outcomes are for requests with every field.
	cfg.MissingFieldShare = 0
	traffic := generate(t, cfg, 2000)
	for _, p := range traffic {
		body, err := json.Marshal(p.Request)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), string(p.Pattern)) {
			t.Fatalf("request includes simulator pattern %s: %s", p.Pattern, body)
		}
	}

	client := &simulator.Client{BaseURL: srv.URL, APIKey: testKey, HTTP: srv.Client()}
	sum, err := simulator.Run(context.Background(), client, traffic, 0, func(err error) { t.Error(err) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sum.Sent != len(traffic) || sum.Failed != 0 {
		t.Fatalf("sent %d, failed %d, want %d sent", sum.Sent, sum.Failed, len(traffic))
	}
	if len(store.saved) != len(traffic) {
		t.Fatalf("saved %d purchases, want %d", len(store.saved), len(traffic))
	}

	want := make(map[simulator.Pattern]map[string]int)
	cardTestingUses := make(map[string]int)
	for i, p := range traffic {
		if store.saved[i].TransactionID != p.Request.TransactionID {
			t.Fatalf("stored purchase %s out of order, want %s", store.saved[i].TransactionID, p.Request.TransactionID)
		}
		result := store.decisions[i].Result
		if want[p.Pattern] == nil {
			want[p.Pattern] = make(map[string]int)
		}
		want[p.Pattern][string(result.Outcome)]++
		switch p.Pattern {
		case simulator.PatternLowAndSlow, simulator.PatternSlowCardTesting, simulator.PatternQuietTakeover:
			if result.Outcome != scoring.OutcomeAllow || len(result.FiredRules) != 0 {
				t.Errorf("%s should pass without firing a rule, got %+v", p.Pattern, result)
			}
		case simulator.PatternCardTesting:
			cardTestingUses[p.Request.CardToken]++
			expected := scoring.OutcomeAllow
			if cardTestingUses[p.Request.CardToken] >= 6 {
				expected = scoring.OutcomeBlock
			}
			if result.Outcome != expected {
				t.Errorf("card testing use %d: got %s, want %s", cardTestingUses[p.Request.CardToken], result.Outcome, expected)
			}
		}
	}
	if len(simulator.Patterns) != 19 || len(want) != 19 {
		t.Fatalf("expected all 19 patterns, got registry %v and outcomes %v", simulator.Patterns, want)
	}
	for _, pattern := range simulator.Patterns {
		if len(want[pattern]) == 0 {
			t.Errorf("missing pattern %s", pattern)
		}
	}
	if want[simulator.PatternCardTesting]["block"] == 0 {
		t.Error("mixed run did not complete a card-testing burst through its block phase")
	}
	farm := want[simulator.PatternDeviceFarm]
	if farm["review"] == 0 || farm["allow"] == 0 || farm["block"] != 0 {
		t.Errorf("device farm outcomes %v, want some review and allow and no block", farm)
	}
	if !reflect.DeepEqual(sum.Outcomes, want) {
		t.Errorf("summary outcomes %v, want %v", sum.Outcomes, want)
	}
	t.Logf("outcomes: %v", sum.Outcomes)
}

func TestRunReportsFailures(t *testing.T) {
	srv, _ := newServer(t)
	traffic := generate(t, simulator.DefaultConfig(), 5)
	conflict := traffic[0]
	conflict.Request.AmountMinor++
	traffic = append(traffic, traffic[0], conflict)

	var errs []error
	client := &simulator.Client{BaseURL: srv.URL, APIKey: testKey, HTTP: srv.Client()}
	sum, err := simulator.Run(context.Background(), client, traffic, 0, func(err error) { errs = append(errs, err) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sum.Sent != 6 || sum.Failed != 1 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "status 409") {
		t.Fatalf("sent %d, failed %d, errors %v; want 6 sent and a conflict for the changed purchase", sum.Sent, sum.Failed, errs)
	}

	bad := &simulator.Client{BaseURL: srv.URL, APIKey: "wrong", HTTP: srv.Client()}
	sum, err = simulator.Run(context.Background(), bad, traffic[:2], 0, nil)
	if err != nil || sum.Failed != 2 {
		t.Fatalf("failed %d, error %v with a wrong API key; want 2 failed and no error", sum.Failed, err)
	}
}

func TestRunGivesUpAfterManyFailuresInRow(t *testing.T) {
	srv, _ := newServer(t)
	traffic := generate(t, simulator.DefaultConfig(), 100)
	bad := &simulator.Client{BaseURL: srv.URL + "/wrong", APIKey: testKey, HTTP: srv.Client()}
	sum, err := simulator.Run(context.Background(), bad, traffic, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("Run returned %v, want an error about status 404", err)
	}
	if sum.Failed != simulator.MaxFailuresInRow {
		t.Fatalf("failed %d, want to stop after %d", sum.Failed, simulator.MaxFailuresInRow)
	}
}

type countingScorer struct{ calls int }

func (c *countingScorer) Score(context.Context, simulator.Request) (simulator.Result, error) {
	c.calls++
	return simulator.Result{Outcome: "allow"}, nil
}

func TestRunKeepsTheRate(t *testing.T) {
	traffic := generate(t, simulator.DefaultConfig(), 11)
	s := &countingScorer{}
	started := time.Now()
	if _, err := simulator.Run(context.Background(), s, traffic, 100, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 11 purchases at 100 per second take at least 10 intervals of 10ms.
	if elapsed := time.Since(started); elapsed < 90*time.Millisecond {
		t.Fatalf("sent 11 purchases in %v, faster than 100 per second", elapsed)
	}
	if s.calls != 11 {
		t.Fatalf("sent %d purchases, want 11", s.calls)
	}
}

func TestRunStopsWhenCancelled(t *testing.T) {
	traffic := generate(t, simulator.DefaultConfig(), 50)
	ctx, cancel := context.WithCancel(context.Background())
	s := &cancellingScorer{cancel: cancel, after: 3}
	sum, err := simulator.Run(ctx, s, traffic, 1000, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if sum.Sent != 3 {
		t.Fatalf("sent %d purchases, want 3", sum.Sent)
	}
	if _, err := simulator.Run(context.Background(), s, traffic, -1, nil); err == nil {
		t.Fatal("Run accepted a negative rate")
	}
}

type cancellingScorer struct {
	cancel func()
	after  int
	calls  int
}

func (c *cancellingScorer) Score(context.Context, simulator.Request) (simulator.Result, error) {
	c.calls++
	if c.calls == c.after {
		c.cancel()
	}
	return simulator.Result{Outcome: "allow"}, nil
}
