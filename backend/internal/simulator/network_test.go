package simulator_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

// flaggedShares returns, per pattern, the share of purchases whose address
// the default mock reports as hosting, and as a VPN, proxy, or Tor exit.
func flaggedShares(t *testing.T, purchases []simulator.Purchase) map[simulator.Pattern][2]float64 {
	t.Helper()
	mock := ipintel.NewMock(ipintel.DefaultRanges)
	flagged := map[simulator.Pattern][2]int{}
	total := map[simulator.Pattern]int{}
	for _, p := range purchases {
		result, err := mock.Lookup(context.Background(), netip.MustParseAddr(*p.Request.IPAddress))
		if err != nil {
			t.Fatal(err)
		}
		counts := flagged[p.Pattern]
		if result.Hosting {
			counts[0]++
		}
		if result.VPN || result.Proxy || result.Tor {
			counts[1]++
		}
		flagged[p.Pattern] = counts
		total[p.Pattern]++
	}
	shares := map[simulator.Pattern][2]float64{}
	for pattern, counts := range flagged {
		shares[pattern] = [2]float64{float64(counts[0]) / float64(total[pattern]), float64(counts[1]) / float64(total[pattern])}
	}
	return shares
}

func TestNetworkPlacementFollowsThePattern(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.6
	cfg.MissingFieldShare = 0
	shares := flaggedShares(t, generate(t, cfg, 20000))

	tests := []struct {
		pattern            simulator.Pattern
		hosting, anonymous [2]float64 // lowest and highest expected share
	}{
		{simulator.PatternNormal, [2]float64{0, 0}, [2]float64{0.005, 0.08}},
		{simulator.PatternTraveler, [2]float64{0, 0}, [2]float64{0.1, 0.35}},
		{simulator.PatternInternetCafe, [2]float64{0, 0.15}, [2]float64{0.08, 0.4}},
		{simulator.PatternCardTesting, [2]float64{0.25, 0.65}, [2]float64{0.08, 0.35}},
		{simulator.PatternDeviceFarm, [2]float64{0.3, 0.7}, [2]float64{0, 0.25}},
		{simulator.PatternAccountTakeover, [2]float64{0, 0}, [2]float64{0.3, 0.6}},
	}
	for _, tt := range tests {
		got := shares[tt.pattern]
		for i, want := range [][2]float64{tt.hosting, tt.anonymous} {
			if got[i] < want[0] || got[i] > want[1] {
				t.Errorf("%s: %.3f of purchases are %s, want %.2f to %.2f", tt.pattern, got[i], []string{"hosting", "anonymous"}[i], want[0], want[1])
			}
		}
	}
}

func TestPlacedAddressesAreFlaggedInBothIPVersions(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.6
	cfg.MissingFieldShare = 0
	mock := ipintel.NewMock(ipintel.DefaultRanges)
	versions := map[string]int{}
	for _, p := range generate(t, cfg, 20000) {
		ip := *p.Request.IPAddress
		version := "IPv4"
		if strings.HasPrefix(ip, "2001:db8:") {
			version = "IPv6"
		} else if !strings.HasPrefix(ip, "192.0.2.") {
			continue
		}
		versions[version]++
		result, err := mock.Lookup(context.Background(), netip.MustParseAddr(ip))
		if err != nil || !(result.Hosting || result.VPN || result.Proxy || result.Tor) {
			t.Fatalf("placed address %s is not flagged: %+v, %v", ip, result, err)
		}
	}
	if versions["IPv4"] == 0 || versions["IPv6"] == 0 {
		t.Errorf("placed addresses by version = %v, want both", versions)
	}
}
