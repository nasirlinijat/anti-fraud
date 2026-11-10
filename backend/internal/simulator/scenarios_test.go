package simulator

import (
	"slices"
	"testing"
	"time"
)

func TestScenariosReuseRegularProfiles(t *testing.T) {
	t.Run("traveler", func(t *testing.T) {
		g := generateScenario(t, PatternTraveler, (*generator).traveler, 1, 1)
		p, req := g.players[0], g.pending[0].Request
		checkSameAccount(t, p, req)
		checkSameCard(t, p.card, req)
		if *req.DeviceID != p.deviceID || *req.DeviceType != p.deviceType || *req.DeviceName != p.deviceName {
			t.Fatalf("traveler changed devices: %+v", req)
		}
		if *req.IPCountry == p.country || *req.IPAddress == p.ipAddress {
			t.Fatalf("traveler used their home connection: %+v", req)
		}
		checkRegularAmount(t, req.AmountMinor)
	})

	t.Run("new phone", func(t *testing.T) {
		g := generateScenario(t, PatternNewPhone, (*generator).newPhone, 1, 1)
		p, req := g.players[0], g.pending[0].Request
		checkSameAccount(t, p, req)
		checkSameCard(t, p.card, req)
		if *req.DeviceType != "mobile" || *req.DeviceID == p.deviceID || !slices.Contains(mobileDevices, *req.DeviceName) {
			t.Fatalf("purchase did not use a new phone: %+v", req)
		}
		if *req.IPCountry != p.country || *req.IPAddress != p.ipAddress {
			t.Fatalf("new phone changed the home connection: %+v", req)
		}
		checkRegularAmount(t, req.AmountMinor)
	})

	t.Run("stolen card", func(t *testing.T) {
		g := generateScenario(t, PatternStolenCard, (*generator).stolenCard, 1, 1)
		owner, req := g.players[0], g.pending[0].Request
		checkSameCard(t, owner.card, req)
		if req.PlayerID == owner.id || *req.DeviceID == owner.deviceID || *req.IPAddress == owner.ipAddress {
			t.Fatalf("buyer reused the card owner's account, device, or connection: %+v", req)
		}
		checkRange(t, "amount", req.AmountMinor, 1499, 24999)
		checkRange(t, "account age at start", g.cfg.Start.Sub(*req.AccountCreatedAt), 30*24*time.Hour, 900*24*time.Hour)
	})
}

func TestScenariosWithMultiplePurchases(t *testing.T) {
	t.Run("internet cafe", func(t *testing.T) {
		g := generateScenario(t, PatternInternetCafe, (*generator).internetCafe, 2, 3)
		first := g.pending[0].Request
		players, cards := map[string]bool{}, map[string]bool{}
		for i, p := range g.pending {
			req := p.Request
			if players[req.PlayerID] || cards[req.CardToken] {
				t.Fatalf("cafe customer reused a player or card: %+v", req)
			}
			players[req.PlayerID], cards[req.CardToken] = true, true
			if *req.DeviceType != "desktop" || *req.DeviceID != *first.DeviceID || *req.DeviceName != *first.DeviceName ||
				*req.IPAddress != *first.IPAddress || *req.IPCountry != *first.IPCountry {
				t.Fatalf("cafe purchase did not use the shared terminal and connection: %+v", req)
			}
			if *req.CardCountry != *req.IPCountry {
				t.Fatalf("cafe customer used a card from outside the cafe's country: %+v", req)
			}
			checkRange(t, "account age at start", g.cfg.Start.Sub(*req.AccountCreatedAt), 30*24*time.Hour, 900*24*time.Hour)
			if i > 0 {
				checkRange(t, "cafe gap", req.EventTime.Sub(g.pending[i-1].Request.EventTime), 15*time.Second, 90*time.Second)
			}
		}
	})

	t.Run("burst buyer", func(t *testing.T) {
		g := generateScenario(t, PatternBurstBuyer, (*generator).burstBuyer, 4, 7)
		first := g.pending[0].Request
		if _, tracked := g.recent[first.CardToken]; tracked {
			t.Fatal("burst uses a regular card and can add future timestamps to its pacing history")
		}
		for i, p := range g.pending {
			req := p.Request
			if req.PlayerID != first.PlayerID || req.CardToken != first.CardToken || *req.DeviceID != *first.DeviceID ||
				!req.AccountCreatedAt.Equal(*first.AccountCreatedAt) {
				t.Fatalf("burst purchase changed the shopper's identity: %+v", req)
			}
			checkRange(t, "amount", req.AmountMinor, 199, 1999)
			checkRange(t, "account age at start", g.cfg.Start.Sub(*req.AccountCreatedAt), 30*24*time.Hour, 900*24*time.Hour)
			if i > 0 {
				checkRange(t, "burst gap", req.EventTime.Sub(g.pending[i-1].Request.EventTime), 10*time.Second, 45*time.Second)
			}
		}
	})
}

func TestSinglePurchaseFromHome(t *testing.T) {
	for _, tc := range []struct {
		pattern    Pattern
		generate   func(*generator, time.Time)
		minAge     time.Duration
		maxAge     time.Duration
		maxAmount  int64
		ageAtStart bool
	}{
		{PatternNewCasualPlayer, (*generator).newCasualPlayer, 30 * time.Minute, 6 * time.Hour, 1999, false},
		{PatternReturningPlayer, (*generator).returningPlayer, 180 * 24 * time.Hour, 900 * 24 * time.Hour, 9999, true},
		{PatternLowAndSlow, (*generator).lowAndSlow, 365 * 24 * time.Hour, 900 * 24 * time.Hour, 9999, true},
	} {
		t.Run(string(tc.pattern), func(t *testing.T) {
			g := generateScenario(t, tc.pattern, tc.generate, 1, 1)
			req := g.pending[0].Request
			if *req.IPCountry != *req.CardCountry || req.PlayerID == g.players[0].id || req.CardToken == g.players[0].card.token {
				t.Fatalf("expected a separate player with their own card and home connection: %+v", req)
			}
			ageAt := req.EventTime
			if tc.ageAtStart {
				ageAt = g.cfg.Start
			}
			checkRange(t, "account age", ageAt.Sub(*req.AccountCreatedAt), tc.minAge, tc.maxAge)
			checkRange(t, "amount", req.AmountMinor, 199, tc.maxAmount)
		})
	}
}

func generateScenario(t *testing.T, pattern Pattern, generate func(*generator, time.Time), minCount, maxCount int) *generator {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Players = 1
	g, err := newGenerator(t.Context(), cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	at := cfg.Start.Add(2 * time.Hour)
	generate(g, at)
	checkRange(t, "purchase count", len(g.pending), minCount, maxCount)
	if got := g.pending[0].Request.EventTime; !got.Equal(at) {
		t.Fatalf("first purchase at %v, want %v", got, at)
	}
	for _, p := range g.pending {
		if p.Pattern != pattern {
			t.Fatalf("generated pattern %q, want %q", p.Pattern, pattern)
		}
	}
	return g
}

func checkSameAccount(t *testing.T, p player, req Request) {
	t.Helper()
	if req.PlayerID != p.id || *req.BillingRegion != p.region || *req.EmailDomain != p.email || !req.AccountCreatedAt.Equal(p.createdAt) {
		t.Fatalf("purchase changed the account profile: %+v", req)
	}
}

func checkSameCard(t *testing.T, want card, req Request) {
	t.Helper()
	got := card{
		token:    req.CardToken,
		issuer:   *req.CardIssuer,
		network:  *req.CardNetwork,
		cardType: *req.CardType,
		country:  *req.CardCountry,
		category: *req.CardCategory,
	}
	if got != want {
		t.Fatalf("card profile %+v, want %+v", got, want)
	}
}

func checkRegularAmount(t *testing.T, amount int64) {
	t.Helper()
	if amount >= 50000 {
		checkRange(t, "premium amount", amount, 50000, 80000)
	} else {
		checkRange(t, "ordinary amount", amount, 199, 9999)
	}
}

func checkRange[T ~int | ~int64](t *testing.T, what string, got, low, high T) {
	t.Helper()
	if got < low || got > high {
		t.Fatalf("%s = %v, want %v to %v", what, got, low, high)
	}
}
