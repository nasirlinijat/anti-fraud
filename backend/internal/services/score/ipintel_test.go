package score_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"go.uber.org/mock/gomock"
)

// fakeIPIntel answers every lookup with result and err after delay. A delay
// longer than the lookup timeout ignores the context, like a stuck client. A
// non-empty panicWith makes the lookup panic instead.
type fakeIPIntel struct {
	result    ipintel.Result
	err       error
	delay     time.Duration
	panicWith string
	calls     atomic.Int32
}

func (f *fakeIPIntel) Name() string { return "fake" }

func (f *fakeIPIntel) Lookup(_ context.Context, _ netip.Addr) (ipintel.Result, error) {
	f.calls.Add(1)
	if f.panicWith != "" {
		panic(f.panicWith)
	}
	time.Sleep(f.delay)
	return f.result, f.err
}

func ipRuleset() scoring.Ruleset {
	return scoring.Ruleset{
		Version:         9,
		ReviewThreshold: 50,
		BlockThreshold:  80,
		Rules: map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleDatacenterIP: {Enabled: true, Weight: 40},
			scoring.RuleAnonymousIP:  {Enabled: true, Weight: 25},
		},
		Model: scoring.ModelSettings{Mode: scoring.ModelOff},
	}
}

func purchaseFrom(ip string) models.Purchase {
	p := purchaseOf(100)
	if ip != "" {
		addr := netip.MustParseAddr(ip)
		p.IPAddress = &addr
	}
	return p
}

// expectScoredAndSaved sets up a purchase that is scored with ipRuleset and
// saved, and returns the saved decision once Score has run.
func (d scoreDeps) expectScoredAndSaved(p models.Purchase) *models.Decision {
	saved := &models.Decision{}
	d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
	d.rulesets.EXPECT().Active(gomock.Any()).Return(ipRuleset(), nil)
	d.blocklist.EXPECT().Match(gomock.Any(), gomock.Any()).Return(models.BlocklistEntry{}, false, nil)
	d.purchases.EXPECT().FeatureHistory(gomock.Any(), gomock.Any()).Return(features.History{}, nil)
	d.purchases.EXPECT().CardPurchasesLastHour(gomock.Any(), p.CardToken, p.EventTime).Return(models.CardVelocityHistory{}, nil)
	d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ models.Purchase, decision models.Decision) (int64, error) {
			*saved = decision
			return 1, nil
		})
	return saved
}

func TestScoreUsesTheIPLookup(t *testing.T) {
	tests := []struct {
		name      string
		found     ipintel.Result
		wantRules []scoring.RuleName
		wantScore int
	}{
		{"ordinary address", ipintel.Result{}, nil, 0},
		{"hosting address", ipintel.Result{Hosting: true}, []scoring.RuleName{scoring.RuleDatacenterIP}, 40},
		{"Tor exit", ipintel.Result{Tor: true}, []scoring.RuleName{scoring.RuleAnonymousIP}, 25},
		{"VPN in a data centre", ipintel.Result{Hosting: true, VPN: true}, []scoring.RuleName{scoring.RuleDatacenterIP, scoring.RuleAnonymousIP}, 65},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newScoreDeps(t)
			provider := &fakeIPIntel{result: tt.found}
			d.service.UseIPIntel(provider)
			p := purchaseFrom("192.0.2.10")
			saved := d.expectScoredAndSaved(p)

			got, err := d.service.Score(context.Background(), p, time.Now())

			if err != nil {
				t.Fatal(err)
			}
			if names := firedNames(got.Result.FiredRules); !reflect.DeepEqual(names, tt.wantRules) || got.Result.Score != tt.wantScore {
				t.Errorf("fired %v for %d points, want %v for %d", names, got.Result.Score, tt.wantRules, tt.wantScore)
			}
			want := tt.found
			want.Provider, want.Available = "fake", true
			if saved.IPIntel == nil || *saved.IPIntel != want {
				t.Errorf("saved IP intelligence = %+v, want %+v", saved.IPIntel, want)
			}
		})
	}
}

func TestScoreContinuesWhenTheIPLookupFails(t *testing.T) {
	tests := []struct {
		name     string
		provider *fakeIPIntel
	}{
		{"provider error", &fakeIPIntel{err: errors.New("reputation service unavailable")}},
		{"provider slower than the timeout", &fakeIPIntel{result: ipintel.Result{Hosting: true}, delay: time.Second}},
		{"provider panics", &fakeIPIntel{panicWith: "nil map in the provider"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newScoreDeps(t)
			d.service.UseIPIntel(tt.provider)
			p := purchaseFrom("192.0.2.10")
			saved := d.expectScoredAndSaved(p)

			started := time.Now()
			got, err := d.service.Score(context.Background(), p, started)
			elapsed := time.Since(started)

			if err != nil || got.Result.Outcome != scoring.OutcomeAllow || len(got.Result.FiredRules) != 0 {
				t.Fatalf("Score = %+v, %v; want an allow with no fired rules", got.Result, err)
			}
			if elapsed > 500*time.Millisecond {
				t.Errorf("Score took %v, want the lookup cut short", elapsed)
			}
			if want := ipintel.Unavailable("fake"); saved.IPIntel == nil || *saved.IPIntel != want {
				t.Errorf("saved IP intelligence = %+v, want %+v", saved.IPIntel, want)
			}
		})
	}
}

func TestScoreMakesNoIPLookupWithoutAnAddressOrAProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider *fakeIPIntel
		ip       string
	}{
		{"no IP address", &fakeIPIntel{result: ipintel.Result{Hosting: true}}, ""},
		{"no provider", nil, "192.0.2.10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newScoreDeps(t)
			if tt.provider != nil {
				d.service.UseIPIntel(tt.provider)
			}
			p := purchaseFrom(tt.ip)
			saved := d.expectScoredAndSaved(p)

			if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
				t.Fatal(err)
			}

			if saved.IPIntel != nil {
				t.Errorf("saved IP intelligence = %+v, want none", saved.IPIntel)
			}
			if tt.provider != nil && tt.provider.calls.Load() != 0 {
				t.Errorf("provider was called %d times, want 0", tt.provider.calls.Load())
			}
		})
	}
}

func TestScoreGivesTheModelTheKindOfNetwork(t *testing.T) {
	tests := []struct {
		name                       string
		provider                   *fakeIPIntel
		ip                         string
		wantHosting, wantAnonymous float64
	}{
		{"ordinary address", &fakeIPIntel{}, "192.0.2.10", 0, 0},
		{"hosting address", &fakeIPIntel{result: ipintel.Result{Hosting: true}}, "192.0.2.10", 1, 0},
		{"proxy", &fakeIPIntel{result: ipintel.Result{Proxy: true}}, "192.0.2.10", 0, 1},
		{"VPN in a data centre", &fakeIPIntel{result: ipintel.Result{Hosting: true, VPN: true}}, "192.0.2.10", 1, 1},
		{"failed lookup", &fakeIPIntel{err: errors.New("reputation service unavailable")}, "192.0.2.10", features.Missing, features.Missing},
		{"no IP address", &fakeIPIntel{result: ipintel.Result{Hosting: true}}, "", features.Missing, features.Missing},
		{"no provider", nil, "192.0.2.10", features.Missing, features.Missing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newScoreDeps(t)
			if tt.provider != nil {
				d.service.UseIPIntel(tt.provider)
			}
			p := purchaseFrom(tt.ip)
			saved := d.expectScoredAndSaved(p)

			if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
				t.Fatal(err)
			}

			hosting, anonymous := saved.ModelInputs["ip_is_hosting"], saved.ModelInputs["ip_is_anonymous"]
			if hosting != tt.wantHosting || anonymous != tt.wantAnonymous {
				t.Errorf("ip_is_hosting %v, ip_is_anonymous %v; want %v, %v", hosting, anonymous, tt.wantHosting, tt.wantAnonymous)
			}
		})
	}
}

func TestScoreGivesTheModelThePaymentVerificationResults(t *testing.T) {
	d := newScoreDeps(t)
	p := purchaseFrom("")
	cvv, threeDS := scoring.CVVMismatch, scoring.ThreeDSAttempted
	p.CVVResult, p.ThreeDSResult = &cvv, &threeDS
	saved := d.expectScoredAndSaved(p)

	if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
		t.Fatal(err)
	}

	want := map[string]float64{"cvv_result_code": 2, "avs_result_code": features.Missing, "three_ds_result_code": 1}
	for name, w := range want {
		if got := saved.ModelInputs[name]; got != w {
			t.Errorf("%s = %v, want %v", name, got, w)
		}
	}
}

func TestScoreRetryMakesNoIPLookup(t *testing.T) {
	d := newScoreDeps(t)
	provider := &fakeIPIntel{result: ipintel.Result{Hosting: true}}
	d.service.UseIPIntel(provider)
	p := purchaseFrom("192.0.2.10")
	saved := models.Decision{RulesetVersion: 9, Result: scoring.Result{Outcome: scoring.OutcomeReview, Score: 40}}
	d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(&saved, nil)

	got, err := d.service.Score(context.Background(), p, time.Now())

	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("Score = %+v, %v; want the saved decision", got, err)
	}
	if provider.calls.Load() != 0 {
		t.Errorf("provider was called %d times on a retry, want 0", provider.calls.Load())
	}
}

// captureLogs sends the default logger's output to the returned buffer until
// the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

func TestScoreLogsAPanickingProviderAsAFailedLookup(t *testing.T) {
	logs := captureLogs(t)
	d := newScoreDeps(t)
	d.service.UseIPIntel(&fakeIPIntel{panicWith: "nil map in the provider"})
	p := purchaseFrom("192.0.2.10")
	d.expectScoredAndSaved(p)

	if _, err := d.service.Score(context.Background(), p, time.Now()); err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(logs.Bytes(), []byte("IP intelligence lookup failed")) || !bytes.Contains(logs.Bytes(), []byte("panicked: nil map in the provider")) {
		t.Errorf("logs = %q, want a failed lookup naming the panic", logs.String())
	}
}

func TestScoreDoesNotBlameTheProviderForAnEndedRequest(t *testing.T) {
	logs := captureLogs(t)
	d := newScoreDeps(t)
	d.service.UseIPIntel(ipintel.NewMock(ipintel.DefaultRanges))
	p := purchaseFrom("192.0.2.10")
	saved := d.expectScoredAndSaved(p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := d.service.Score(ctx, p, time.Now()); err != nil {
		t.Fatal(err)
	}

	if want := ipintel.Unavailable(ipintel.MockName); saved.IPIntel == nil || *saved.IPIntel != want {
		t.Errorf("saved IP intelligence = %+v, want %+v", saved.IPIntel, want)
	}
	if logs.Len() != 0 {
		t.Errorf("logs = %q, want nothing for a request that ended", logs.String())
	}
}

func TestScoreSavesTheIPLookupOfABlocklistedPurchase(t *testing.T) {
	d := newScoreDeps(t)
	d.service.UseIPIntel(&fakeIPIntel{result: ipintel.Result{Hosting: true}})
	p := purchaseFrom("192.0.2.10")
	var saved models.Decision
	d.purchases.EXPECT().FindSavedDecision(gomock.Any(), p).Return(nil, nil)
	d.rulesets.EXPECT().Active(gomock.Any()).Return(ipRuleset(), nil)
	d.blocklist.EXPECT().Match(gomock.Any(), gomock.Any()).Return(models.BlocklistEntry{Type: models.BlocklistCard, Value: p.CardToken}, true, nil)
	d.purchases.EXPECT().FeatureHistory(gomock.Any(), gomock.Any()).Return(features.History{}, nil)
	d.purchases.EXPECT().Save(gomock.Any(), p, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ models.Purchase, decision models.Decision) (int64, error) {
			saved = decision
			return 1, nil
		})

	got, err := d.service.Score(context.Background(), p, time.Now())

	if err != nil || got.Result.Outcome != scoring.OutcomeBlock {
		t.Fatalf("Score = %+v, %v; want a block", got.Result, err)
	}
	want := ipintel.Result{Provider: "fake", Available: true, Hosting: true}
	if saved.IPIntel == nil || *saved.IPIntel != want {
		t.Errorf("saved IP intelligence = %+v, want %+v", saved.IPIntel, want)
	}
}

func firedNames(rules []scoring.FiredRule) []scoring.RuleName {
	var names []scoring.RuleName
	for _, r := range rules {
		names = append(names, r.Name)
	}
	return names
}
