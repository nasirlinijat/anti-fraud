package simulator_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

// The hashes are of the JSON lines that the generator wrote for these seeds
// before it sent verification results. Removing the results must give the
// same bytes, so existing seeds keep their traffic.
func TestVerificationLeavesTheRestOfTheTrafficUnchanged(t *testing.T) {
	tests := []struct {
		seed uint64
		want string
	}{
		{1, "8a84902c5c79b65a13e07076730aa322d1e576815ea1e37e692e842a80ff3f13"},
		{7, "afdfde2ed32527a1ee5a6268e853e7124a013017048dfc3b9ec3f3f3865655d1"},
	}
	for _, tt := range tests {
		cfg := simulator.DefaultConfig()
		cfg.Seed = tt.seed
		cfg.SuspiciousShare = 0.3
		purchases := generate(t, cfg, 5000)
		for i := range purchases {
			purchases[i].Request.CVVResult, purchases[i].Request.AVSResult, purchases[i].Request.ThreeDSResult = nil, nil, nil
		}
		var buf bytes.Buffer
		if err := simulator.WriteJSONL(&buf, purchases); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(buf.Bytes())
		if got := hex.EncodeToString(sum[:]); got != tt.want {
			t.Errorf("seed %d: traffic without verification results hashes to %s, want %s", tt.seed, got, tt.want)
		}
	}
}

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
		{simulator.PatternCardTesting, [3]float64{0.3, 0.35, 0.2}, [3]float64{0.6, 0.65, 0.5}},
		{simulator.PatternStolenCard, [3]float64{0.02, 0.15, 0.25}, [3]float64{0.2, 0.45, 0.55}},
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
