package scoring

import (
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	event := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	created := event.Add(-2 * time.Hour)
	ip := "TR"

	purchase := Input{
		AmountMinor:           60000,
		CardCountry:           "AZ",
		IPCountry:             &ip,
		AccountCreatedAt:      &created,
		EventTime:             event,
		CardPurchasesLastHour: 1,
	}

	allRules := func() map[RuleName]RuleConfig {
		return map[RuleName]RuleConfig{
			RuleHighAmount:      {Enabled: true, Weight: 30, Limit: 50000},
			RuleCardVelocity:    {Enabled: true, Weight: 40, Limit: 5},
			RuleNewAccount:      {Enabled: true, Weight: 20, Limit: 24},
			RuleCountryMismatch: {Enabled: true, Weight: 25},
		}
	}

	disabled := allRules()
	disabled[RuleNewAccount] = RuleConfig{Enabled: false, Weight: 20, Limit: 24}

	missing := allRules()
	delete(missing, RuleCountryMismatch)

	tests := []struct {
		name      string
		in        Input
		rules     map[RuleName]RuleConfig
		wantScore int
		wantOut   Outcome
		wantFired []FiredRule
	}{
		{
			name:      "three rules fire",
			in:        purchase,
			rules:     allRules(),
			wantScore: 75,
			wantOut:   OutcomeReview,
			wantFired: []FiredRule{
				{Name: RuleHighAmount, Points: 30, Message: "Amount 60000 minor units is at or above the limit of 50000"},
				{Name: RuleNewAccount, Points: 20, Message: "Account is 2 hours old, under the limit of 24 hours"},
				{Name: RuleCountryMismatch, Points: 25, Message: "IP country TR differs from card country AZ"},
			},
		},
		{
			name:      "disabled rule is skipped",
			in:        purchase,
			rules:     disabled,
			wantScore: 55,
			wantOut:   OutcomeReview,
			wantFired: []FiredRule{
				{Name: RuleHighAmount, Points: 30, Message: "Amount 60000 minor units is at or above the limit of 50000"},
				{Name: RuleCountryMismatch, Points: 25, Message: "IP country TR differs from card country AZ"},
			},
		},
		{
			name:      "missing rule is skipped",
			in:        purchase,
			rules:     missing,
			wantScore: 50,
			wantOut:   OutcomeReview,
			wantFired: []FiredRule{
				{Name: RuleHighAmount, Points: 30, Message: "Amount 60000 minor units is at or above the limit of 50000"},
				{Name: RuleNewAccount, Points: 20, Message: "Account is 2 hours old, under the limit of 24 hours"},
			},
		},
		{
			name:      "no rule fires",
			in:        Input{AmountMinor: 1000, CardCountry: "AZ", EventTime: event},
			rules:     allRules(),
			wantScore: 0,
			wantOut:   OutcomeAllow,
			wantFired: nil,
		},
	}

	for _, tt := range tests {
		rs := Ruleset{ReviewThreshold: 50, BlockThreshold: 80, Rules: tt.rules}
		got := Evaluate(tt.in, rs)
		if got.Score != tt.wantScore || got.Outcome != tt.wantOut || !reflect.DeepEqual(got.FiredRules, tt.wantFired) {
			t.Errorf("%s: got %+v, want score %d, outcome %q, fired %+v",
				tt.name, got, tt.wantScore, tt.wantOut, tt.wantFired)
		}
	}
}

func TestDecide(t *testing.T) {
	rs := Ruleset{ReviewThreshold: 50, BlockThreshold: 80}

	tests := []struct {
		score int
		want  Outcome
	}{
		{0, OutcomeAllow},
		{49, OutcomeAllow},
		{50, OutcomeReview},
		{79, OutcomeReview},
		{80, OutcomeBlock},
		{120, OutcomeBlock},
	}

	for _, tt := range tests {
		if got := decide(tt.score, rs); got != tt.want {
			t.Errorf("decide(%d) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestDeviceCardCount(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int64
		limit int64
		want  string
	}{
		{"missing device", 0, 1, ""},
		{"below limit", 4, 5, ""},
		{"at limit", 5, 5, "Device used 5 distinct cards in 60 minutes (limit 5)"},
		{"above limit", 6, 5, "Device used 6 distinct cards in 60 minutes (limit 5)"},
		{"one card limit", 1, 1, "Device used 1 distinct card in 60 minutes (limit 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := Ruleset{
				ReviewThreshold: 15, BlockThreshold: 80,
				Rules: map[RuleName]RuleConfig{RuleDeviceCardCount: {Enabled: true, Weight: 17, Limit: tc.limit}},
			}
			want := Result{Outcome: OutcomeAllow}
			if tc.want != "" {
				want = Result{
					Outcome: OutcomeReview, Score: 17,
					FiredRules: []FiredRule{{Name: RuleDeviceCardCount, Points: 17, Message: tc.want}},
				}
			}
			if got := Evaluate(Input{DeviceCardsLastHour: tc.count}, rs); !reflect.DeepEqual(got, want) {
				t.Errorf("result = %+v, want %+v", got, want)
			}
		})
	}
}

func TestNewDeviceSpike(t *testing.T) {
	cfg := RuleConfig{Enabled: true, Weight: 50, Limit: 3, WindowDays: 90, MinAmountMinor: 1000, AmountMultiplier: 3}
	rs := Ruleset{ReviewThreshold: 50, BlockThreshold: 80, Rules: map[RuleName]RuleConfig{RuleNewDeviceSpike: cfg}}
	history := func(count, total int64) *TrustedPurchaseHistory {
		return &TrustedPurchaseHistory{Count: count, TotalMinor: big.NewInt(total)}
	}
	known := history(3, 900)
	known.DeviceSeen = true
	large := &TrustedPurchaseHistory{Count: 6, TotalMinor: new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(2))}
	for _, tc := range []struct {
		name    string
		amount  int64
		history *TrustedPurchaseHistory
		want    bool
	}{
		{"missing device", 1200, nil, false},
		{"insufficient history", 1200, history(2, 500), false},
		{"known device", 1200, known, false},
		{"below absolute floor", 999, history(3, 900), false},
		{"exact absolute floor", 1000, history(3, 900), true},
		{"below relative threshold", 1199, history(3, 1200), false},
		{"exact relative threshold", 1200, history(3, 1200), true},
		{"below fractional threshold", 1000, history(4, 1334), false},
		{"above fractional threshold", 1001, history(4, 1334), true},
		{"large below threshold", math.MaxInt64 - 1, large, false},
		{"large at threshold", math.MaxInt64, large, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(Input{AmountMinor: tc.amount, TrustedHistory: tc.history}, rs)
			if !tc.want {
				if got.Score != 0 || got.Outcome != OutcomeAllow || len(got.FiredRules) != 0 {
					t.Errorf("unexpected fired rule: %+v", got)
				}
				return
			}
			if got.Score != cfg.Weight || got.Outcome != OutcomeReview || len(got.FiredRules) != 1 ||
				got.FiredRules[0].Name != RuleNewDeviceSpike || got.FiredRules[0].Points != cfg.Weight || got.FiredRules[0].Message == "" {
				t.Errorf("result = %+v, want new device spike with %d points and a message", got, cfg.Weight)
			}
		})
	}
}

func TestRuleNameKnown(t *testing.T) {
	tests := []struct {
		name RuleName
		want bool
	}{
		{RuleHighAmount, true},
		{RuleCardVelocity, true},
		{RuleNewAccount, true},
		{RuleCountryMismatch, true},
		{RuleDeviceCardCount, true},
		{RuleNewDeviceSpike, true},
		{RuleCVVMismatch, true},
		{RuleAVSMismatch, true},
		{RuleThreeDSFailed, true},
		{RuleDatacenterIP, true},
		{RuleAnonymousIP, true},
		{"high_ammount", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := tt.name.Known(); got != tt.want {
			t.Errorf("RuleName(%q).Known() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestHighAmount(t *testing.T) {
	tests := []struct {
		amount int64
		want   bool
	}{
		{49999, false},
		{50000, true},
		{60000, true},
	}

	for _, tt := range tests {
		in := Input{AmountMinor: tt.amount}
		if got := highAmount(in, RuleConfig{Limit: 50000}); got != tt.want {
			t.Errorf("highAmount(%d) = %v, want %v", tt.amount, got, tt.want)
		}
	}
}

func TestCardVelocity(t *testing.T) {
	tests := []struct {
		earlier int
		want    bool
	}{
		{0, false},
		{4, false},
		{5, true},
		{9, true},
	}

	for _, tt := range tests {
		in := Input{CardPurchasesLastHour: tt.earlier}
		if got := cardVelocity(in, RuleConfig{Limit: 5}); got != tt.want {
			t.Errorf("cardVelocity(%d earlier) = %v, want %v", tt.earlier, got, tt.want)
		}
	}
}

func TestNewAccount(t *testing.T) {
	event := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	createdAt := func(d time.Duration) *time.Time {
		c := event.Add(-d)
		return &c
	}

	tests := []struct {
		name    string
		created *time.Time
		want    bool
	}{
		{"missing creation time", nil, false},
		{"2 hours old", createdAt(2 * time.Hour), true},
		{"just under 24 hours", createdAt(24*time.Hour - time.Second), true},
		{"exactly 24 hours", createdAt(24 * time.Hour), false},
		{"30 days old", createdAt(30 * 24 * time.Hour), false},
		{"created after the purchase", createdAt(-time.Hour), true},
	}

	for _, tt := range tests {
		in := Input{EventTime: event, AccountCreatedAt: tt.created}
		if got := newAccount(in, RuleConfig{Limit: 24}); got != tt.want {
			t.Errorf("newAccount(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCountryMismatch(t *testing.T) {
	country := func(c string) *string { return &c }

	tests := []struct {
		name string
		card string
		ip   *string
		want bool
	}{
		{"missing IP country", "AZ", nil, false},
		{"missing card country", "", country("TR"), false},
		{"same country", "AZ", country("AZ"), false},
		{"different country", "AZ", country("TR"), true},
	}

	for _, tt := range tests {
		in := Input{CardCountry: tt.card, IPCountry: tt.ip}
		if got := countryMismatch(in, RuleConfig{}); got != tt.want {
			t.Errorf("countryMismatch(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestFiredRuleMessagesStateTheValueAndTheLimit(t *testing.T) {
	event := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	rs := Ruleset{ReviewThreshold: 10, BlockThreshold: 1000, Rules: map[RuleName]RuleConfig{
		RuleCardVelocity: {Enabled: true, Weight: 40, Limit: 5},
	}}
	got := Evaluate(Input{EventTime: event, CardPurchasesLastHour: 7}, rs)
	want := []FiredRule{{Name: RuleCardVelocity, Points: 40, Message: "Card used 7 times in 60 minutes (limit 5)"}}
	if !reflect.DeepEqual(got.FiredRules, want) {
		t.Errorf("fired = %+v, want %+v", got.FiredRules, want)
	}
}

func TestNewAccountMessageNeverShowsNegativeAge(t *testing.T) {
	event := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	created := event.Add(3 * time.Hour) // created after the purchase, for example from a clock skew
	got := newAccountMessage(Input{EventTime: event, AccountCreatedAt: &created}, RuleConfig{Limit: 24})
	if want := "Account is 0 hours old, under the limit of 24 hours"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestBlocklistMatchNamesTheEntryTypeButNotTheValue(t *testing.T) {
	for entryType, want := range map[string]string{
		"card":   "Blocked: the card is on the blocklist",
		"device": "Blocked: the device is on the blocklist",
		"player": "Blocked: the player is on the blocklist",
		"ip":     "Blocked: the IP address is on the blocklist",
	} {
		got := BlocklistMatch(entryType)
		if got.Name != RuleBlocklist || got.Points != 0 || got.Message != want {
			t.Errorf("BlocklistMatch(%q) = %+v, want %q with no points", entryType, got, want)
		}
	}
	if RuleBlocklist.Known() {
		t.Error("blocklist must not be a configurable rule")
	}
}

func TestEveryConfigurableRuleHasAMessage(t *testing.T) {
	for _, r := range rules {
		if r.message == nil {
			t.Errorf("rule %s has no message", r.name)
		}
	}
}

func TestMessagesUseSingularForOne(t *testing.T) {
	event := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	created := event.Add(-time.Hour)
	if got, want := newAccountMessage(Input{EventTime: event, AccountCreatedAt: &created}, RuleConfig{Limit: 1}), "Account is 1 hour old, under the limit of 1 hour"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if got, want := cardVelocityMessage(Input{CardPurchasesLastHour: 1}, RuleConfig{Limit: 1}), "Card used 1 time in 60 minutes (limit 1)"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
