package scoring

import (
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
				{RuleHighAmount, 30},
				{RuleNewAccount, 20},
				{RuleCountryMismatch, 25},
			},
		},
		{
			name:      "disabled rule is skipped",
			in:        purchase,
			rules:     disabled,
			wantScore: 55,
			wantOut:   OutcomeReview,
			wantFired: []FiredRule{
				{RuleHighAmount, 30},
				{RuleCountryMismatch, 25},
			},
		},
		{
			name:      "missing rule is skipped",
			in:        purchase,
			rules:     missing,
			wantScore: 50,
			wantOut:   OutcomeReview,
			wantFired: []FiredRule{
				{RuleHighAmount, 30},
				{RuleNewAccount, 20},
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

func TestRuleNameKnown(t *testing.T) {
	tests := []struct {
		name RuleName
		want bool
	}{
		{RuleHighAmount, true},
		{RuleCardVelocity, true},
		{RuleNewAccount, true},
		{RuleCountryMismatch, true},
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
		if got := highAmount(in, 50000); got != tt.want {
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
		if got := cardVelocity(in, 5); got != tt.want {
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
		if got := newAccount(in, 24); got != tt.want {
			t.Errorf("newAccount(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCountryMismatch(t *testing.T) {
	country := func(c string) *string { return &c }

	tests := []struct {
		name string
		ip   *string
		want bool
	}{
		{"missing IP country", nil, false},
		{"same country", country("AZ"), false},
		{"different country", country("TR"), true},
	}

	for _, tt := range tests {
		in := Input{CardCountry: "AZ", IPCountry: tt.ip}
		if got := countryMismatch(in, 0); got != tt.want {
			t.Errorf("countryMismatch(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
