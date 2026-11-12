package simulator_test

import (
	"reflect"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

// sentGroups reports, for each group of optional fields that the simulator
// always fills, whether the request carries it. A group arrives or is left out
// as a whole, so one field stands for its group.
func sentGroups(req simulator.Request) [8]bool {
	return [8]bool{
		req.IPCountry != nil,
		req.DeviceID != nil,
		req.DeviceName != nil,
		req.AccountCreatedAt != nil,
		req.BillingRegion != nil,
		req.CardIssuer != nil,
		req.EmailDomain != nil,
		req.CVVResult != nil,
	}
}

func mixedTraffic(t *testing.T, missingFieldShare float64) []simulator.Purchase {
	t.Helper()
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.3
	cfg.MissingFieldShare = missingFieldShare
	return generate(t, cfg, 5000)
}

func TestNoFieldIsMissingWithShareZero(t *testing.T) {
	for _, p := range mixedTraffic(t, 0) {
		if sentGroups(p.Request) != [8]bool{true, true, true, true, true, true, true, true} {
			t.Fatalf("purchase %s misses a field group: %+v", p.Request.TransactionID, p.Request)
		}
	}
}

func TestPlayerMissesTheSameFieldGroupsOnEveryPurchase(t *testing.T) {
	groups := map[string][8]bool{}
	for _, p := range mixedTraffic(t, 0.5) {
		got := sentGroups(p.Request)
		if earlier, seen := groups[p.Request.PlayerID]; seen && earlier != got {
			t.Fatalf("player %s sent %v, then %v", p.Request.PlayerID, earlier, got)
		}
		groups[p.Request.PlayerID] = got
	}
}

func TestEachFieldGroupIsMissingForAboutTheShareOfPlayers(t *testing.T) {
	players := map[string][8]bool{}
	for _, p := range mixedTraffic(t, 0.25) {
		players[p.Request.PlayerID] = sentGroups(p.Request)
	}
	for group := range 8 {
		missing := 0
		for _, sent := range players {
			if !sent[group] {
				missing++
			}
		}
		if share := float64(missing) / float64(len(players)); share < 0.15 || share > 0.35 {
			t.Errorf("group %d is missing for %.2f of %d players, want about 0.25", group, share, len(players))
		}
	}
}

// A field group is removed from finished requests only, so the purchases are
// the same as without missing fields, apart from the removed fields.
func TestMissingFieldsChangeNothingElse(t *testing.T) {
	full, thin := mixedTraffic(t, 0), mixedTraffic(t, 0.5)
	removed := 0
	for i := range full {
		if full[i].Pattern != thin[i].Pattern {
			t.Fatalf("purchase %d is %s, then %s", i, full[i].Pattern, thin[i].Pattern)
		}
		want, got := reflect.ValueOf(full[i].Request), reflect.ValueOf(thin[i].Request)
		for field := range want.NumField() {
			if got.Field(field).Kind() == reflect.Pointer && got.Field(field).IsNil() && !want.Field(field).IsNil() {
				removed++
				continue
			}
			if !reflect.DeepEqual(got.Field(field).Interface(), want.Field(field).Interface()) {
				t.Fatalf("purchase %d: %s changed", i, want.Type().Field(field).Name)
			}
		}
	}
	if removed == 0 {
		t.Fatal("no field was removed with share 0.5")
	}
}

func TestFraudAndLegitimatePurchasesMissFieldsAlike(t *testing.T) {
	missing, total := map[bool]int{}, map[bool]int{}
	for _, p := range mixedTraffic(t, 0.5) {
		fraud := p.Pattern.Fraud()
		total[fraud]++
		if p.Request.IPCountry == nil {
			missing[fraud]++
		}
	}
	for _, fraud := range []bool{false, true} {
		if share := float64(missing[fraud]) / float64(total[fraud]); share < 0.35 || share > 0.65 {
			t.Errorf("fraud=%v: IP country is missing in %.2f of %d purchases, want about 0.5", fraud, share, total[fraud])
		}
	}
}
