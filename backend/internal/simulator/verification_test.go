package simulator_test

import (
	"slices"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

func TestVerificationResultsAreValuesTheScoreAPIAccepts(t *testing.T) {
	sent := 0
	for _, p := range mixedTraffic(t, 0.25) {
		req := p.Request
		if req.CVVResult == nil {
			if req.AVSResult != nil || req.ThreeDSResult != nil {
				t.Fatalf("purchase %s sends only some verification results", req.TransactionID)
			}
			continue
		}
		sent++
		if !slices.Contains(scoring.CVVResults, scoring.CVVResult(*req.CVVResult)) ||
			!slices.Contains(scoring.AVSResults, scoring.AVSResult(*req.AVSResult)) ||
			!slices.Contains(scoring.ThreeDSResults, scoring.ThreeDSResult(*req.ThreeDSResult)) {
			t.Fatalf("purchase %s has unknown results %s, %s, %s", req.TransactionID, *req.CVVResult, *req.AVSResult, *req.ThreeDSResult)
		}
	}
	if sent == 0 {
		t.Fatal("no purchase sent verification results")
	}
}

// failureShares returns, per pattern, the share of purchases whose CVV, AVS,
// and 3-D Secure checks failed outright.
func failureShares(purchases []simulator.Purchase) map[simulator.Pattern][3]float64 {
	failed := map[simulator.Pattern][3]int{}
	total := map[simulator.Pattern]int{}
	for _, p := range purchases {
		req := p.Request
		counts := failed[p.Pattern]
		for i, isFailure := range []bool{*req.CVVResult == "mismatch", *req.AVSResult == "mismatch", *req.ThreeDSResult == "failed"} {
			if isFailure {
				counts[i]++
			}
		}
		failed[p.Pattern] = counts
		total[p.Pattern]++
	}
	shares := map[simulator.Pattern][3]float64{}
	for pattern, counts := range failed {
		for i, n := range counts {
			share := shares[pattern]
			share[i] = float64(n) / float64(total[pattern])
			shares[pattern] = share
		}
	}
	return shares
}

func TestVerificationFailuresFollowThePattern(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.6
	cfg.MissingFieldShare = 0
	shares := failureShares(generate(t, cfg, 20000))

	tests := []struct {
		pattern  simulator.Pattern
		min, max [3]float64
	}{
		{simulator.PatternNormal, [3]float64{0, 0, 0}, [3]float64{0.03, 0.05, 0.03}},
		{simulator.PatternTraveler, [3]float64{0, 0, 0}, [3]float64{0.05, 0.08, 0.05}},
		{simulator.PatternCardTesting, [3]float64{0.3, 0.06, 0.2}, [3]float64{0.6, 0.2, 0.5}},
		{simulator.PatternStolenCard, [3]float64{0.02, 0.02, 0.25}, [3]float64{0.2, 0.15, 0.55}},
		{simulator.PatternAccountTakeover, [3]float64{0, 0, 0.03}, [3]float64{0.1, 0.12, 0.3}},
	}
	for _, tt := range tests {
		got := shares[tt.pattern]
		for i, check := range []string{"CVV", "AVS", "3-D Secure"} {
			if got[i] < tt.min[i] || got[i] > tt.max[i] {
				t.Errorf("%s: %s fails for %.3f of purchases, want %.2f to %.2f", tt.pattern, check, got[i], tt.min[i], tt.max[i])
			}
		}
	}
}

func TestOnlyCardsFromAddressCheckCountriesGetAnAddressCheck(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.6
	cfg.MissingFieldShare = 0
	checked := 0
	for _, p := range generate(t, cfg, 5000) {
		req := p.Request
		supported := *req.CardCountry == "US" || *req.CardCountry == "GB"
		if !supported && *req.AVSResult != "not_checked" {
			t.Fatalf("purchase %s with a card from %s has address check result %s", req.TransactionID, *req.CardCountry, *req.AVSResult)
		}
		if supported && *req.AVSResult != "not_checked" {
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no card had its address checked")
	}
}

func TestSavedCardsSkipTheCodeCheckAsOftenForHonestPlayersAsForTakeovers(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.Scenarios = []simulator.Pattern{simulator.PatternNormal, simulator.PatternAccountTakeover, simulator.PatternNewCasualPlayer}
	cfg.SuspiciousShare = 0.5
	cfg.MissingFieldShare = 0
	skipped, total := map[simulator.Pattern]int{}, map[simulator.Pattern]int{}
	for _, p := range generate(t, cfg, 20000) {
		total[p.Pattern]++
		if *p.Request.CVVResult == "not_checked" {
			skipped[p.Pattern]++
		}
	}
	share := func(pattern simulator.Pattern) float64 { return float64(skipped[pattern]) / float64(total[pattern]) }
	if normal, takeover := share(simulator.PatternNormal), share(simulator.PatternAccountTakeover); normal < 0.3 || takeover < 0.3 {
		t.Errorf("the CVV check is skipped for %.2f of normal purchases and %.2f of takeovers, want both about 0.35 to 0.4", normal, takeover)
	}
	if fresh := share(simulator.PatternNewCasualPlayer); fresh > 0.1 {
		t.Errorf("the CVV check is skipped for %.2f of new players' first cards, want about 0.05", fresh)
	}
}
