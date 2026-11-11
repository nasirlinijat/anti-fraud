package simulator

import (
	"math"
	"slices"
	"testing"
	"time"
)

func TestLargeMeanGapKeepsClockMovingForward(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MeanGap = math.MaxInt64
	g, err := newGenerator(t.Context(), cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if gap := g.gap(); gap < time.Second {
			t.Fatalf("simulated clock advanced by %v", gap)
		}
	}
}

func TestGeneratorKeepsPendingPurchasesAndHistoryBounded(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Players = 2
	cfg.MeanGap = time.Second
	cfg.SuspiciousShare = 0.6
	g, err := newGenerator(t.Context(), cfg, false)
	if err != nil {
		t.Fatal(err)
	}

	// The longest scenario is slow card testing, with at most four gaps of up
	// to 45 minutes. Events start at least one second apart and each adds at
	// most nine purchases.
	const maxPending = (4*45*60 + 1) * 9
	const count = 30_000
	var previous Purchase
	overlap := false
	sent := 0
	for p := range g.purchases(t.Context()) {
		if sent > 0 {
			if comparePurchases(previous, p) >= 0 {
				t.Fatalf("purchases out of order: %v before %v", previous.Request, p.Request)
			}
			if compareIDs(previous.Request.TransactionID, p.Request.TransactionID) > 0 {
				overlap = true
			}
		}
		if len(g.pending) > maxPending {
			t.Fatalf("queued %d purchases, want at most %d", len(g.pending), maxPending)
		}
		if len(g.recent) != cfg.Players {
			t.Fatalf("tracking %d cards, want %d regular cards", len(g.recent), cfg.Players)
		}
		for _, times := range g.recent {
			if !slices.IsSortedFunc(times, time.Time.Compare) {
				t.Fatalf("out-of-order card history: %v", times)
			}
			if len(times) > 0 && times[len(times)-1].Sub(times[0]) >= time.Hour {
				t.Fatalf("retained a card's history across %v", times[len(times)-1].Sub(times[0]))
			}
		}
		previous = p
		sent++
		if sent == count {
			break
		}
	}
	if sent != count || !overlap {
		t.Fatalf("got %d purchases with overlapping bursts = %v", sent, overlap)
	}
}
