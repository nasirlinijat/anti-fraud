package simulator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
)

// withoutNetworkPlacement turns off the placement of connections in the IP
// intelligence ranges for one test.
func withoutNetworkPlacement(t *testing.T) {
	t.Helper()
	legitimate, special := legitimateNetworks, patternNetworks
	legitimateNetworks, patternNetworks = nil, nil
	t.Cleanup(func() { legitimateNetworks, patternNetworks = legitimate, special })
}

func goldenTraffic(t *testing.T, seed uint64) []Purchase {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Seed = seed
	cfg.SuspiciousShare = 0.3
	purchases, err := Generate(cfg, 5000)
	if err != nil {
		t.Fatal(err)
	}
	return purchases
}

func hashJSONL(t *testing.T, purchases []Purchase) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, purchases); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// The hashes are of the JSON lines the generator wrote for these seeds before
// it sent verification results (base) and before it placed connections in the
// IP intelligence ranges (verified). Each addition draws from its own seeded
// stream, so removing it gives the earlier bytes and existing seeds keep their
// traffic.
func TestAdditionsLeaveTheEarlierTrafficUnchanged(t *testing.T) {
	withoutNetworkPlacement(t)
	tests := []struct {
		seed           uint64
		verified, base string
	}{
		{1, "d82ab196666ea65a824a7209cc5e6346752c3a4a24f8f6e563e7375b5bb16b75", "8a84902c5c79b65a13e07076730aa322d1e576815ea1e37e692e842a80ff3f13"},
		{7, "2925c2e75fabc3ad9fa8e5aa3711e8b038b3ae449a3e82246e9d832a21352d33", "afdfde2ed32527a1ee5a6268e853e7124a013017048dfc3b9ec3f3f3865655d1"},
	}
	for _, tt := range tests {
		purchases := goldenTraffic(t, tt.seed)
		if got := hashJSONL(t, purchases); got != tt.verified {
			t.Errorf("seed %d: traffic without network placement hashes to %s, want %s", tt.seed, got, tt.verified)
		}
		for i := range purchases {
			purchases[i].Request.CVVResult, purchases[i].Request.AVSResult, purchases[i].Request.ThreeDSResult = nil, nil, nil
		}
		if got := hashJSONL(t, purchases); got != tt.base {
			t.Errorf("seed %d: traffic without verification results hashes to %s, want %s", tt.seed, got, tt.base)
		}
	}
}

func TestNetworkPlacementChangesOnlyTheIPAddress(t *testing.T) {
	placed := goldenTraffic(t, 1)
	withoutNetworkPlacement(t)
	home := goldenTraffic(t, 1)

	moved := 0
	for i := range placed {
		if !reflect.DeepEqual(placed[i].Request.IPAddress, home[i].Request.IPAddress) {
			moved++
		}
		got := placed[i]
		got.Request.IPAddress = home[i].Request.IPAddress
		if !reflect.DeepEqual(got, home[i]) {
			t.Fatalf("purchase %d changed beyond its IP address:\n%+v\nwant\n%+v", i, got, home[i])
		}
	}
	if moved == 0 {
		t.Fatal("no connection was placed in an IP intelligence range")
	}
}

// TestAccountEventsLeaveThePurchasesOfASeedUnchanged compares the dump of the
// default traffic with hashes recorded before the simulator sent account
// events. Account events use their own random stream, so the purchases, their
// IDs and the dump stay byte for byte the same. The hashes predate the
// verification results and the placement of connections in the IP intelligence
// ranges too, so those are removed before hashing.
func TestAccountEventsLeaveThePurchasesOfASeedUnchanged(t *testing.T) {
	withoutNetworkPlacement(t)
	want := map[float64]string{
		0.1: "dd2249f577a0dbfc8c37f9b6a9f5fe6453340ada6f130e3f9ff8a17905c5c53f",
		0.5: "b8633f56c26e0eacf34cf5c1c8ff303e321d9de3691520feac58b898e575c805",
		1:   "7c0f22f1e93fcdd3bbb45e66ddba81d5e671cf021198430c20906166ae77c940",
	}
	for share, hash := range want {
		cfg := DefaultConfig()
		cfg.SuspiciousShare = share
		purchases, err := Generate(cfg, 3000)
		if err != nil {
			t.Fatal(err)
		}
		for i := range purchases {
			purchases[i].Request.CVVResult, purchases[i].Request.AVSResult, purchases[i].Request.ThreeDSResult = nil, nil, nil
		}
		if got := hashJSONL(t, purchases); got != hash {
			t.Errorf("suspicious share %v: dump hash %s, want %s", share, got, hash)
		}
	}
}
