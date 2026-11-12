package scoring

import (
	"reflect"
	"testing"
	"time"
)

var purchaseTime = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func before(d time.Duration) time.Time {
	return purchaseTime.Add(-d)
}

func TestRecentPasswordReset(t *testing.T) {
	reset := func(d time.Duration) *AccountHistory {
		at := before(d)
		return &AccountHistory{LastPasswordReset: &at}
	}
	tests := []struct {
		name    string
		history *AccountHistory
		want    bool
	}{
		{"history not loaded", nil, false},
		{"no password reset", &AccountHistory{}, false},
		{"one minute before", reset(time.Minute), true},
		{"just under 24 hours before", reset(24*time.Hour - time.Second), true},
		{"exactly 24 hours before", reset(24 * time.Hour), false},
		{"two days before", reset(48 * time.Hour), false},
		{"at the purchase time", reset(0), false},
		{"after the purchase", reset(-time.Minute), false},
	}

	for _, tt := range tests {
		in := Input{EventTime: purchaseTime, AccountHistory: tt.history}
		if got := recentPasswordReset(in, RuleConfig{Limit: 24}); got != tt.want {
			t.Errorf("recentPasswordReset(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestNewLoginRules(t *testing.T) {
	tests := []struct {
		name   string
		logins []time.Time
		want   bool
	}{
		{"no new logins", nil, false},
		{"new an hour before", []time.Time{before(time.Hour)}, true},
		{"new just under the window", []time.Time{before(24*time.Hour - time.Second)}, true},
		{"new exactly at the window", []time.Time{before(24 * time.Hour)}, false},
		{"new two days before", []time.Time{before(48 * time.Hour)}, false},
		{"new at the purchase time", []time.Time{purchaseTime}, false},
		{"new after the purchase", []time.Time{before(-time.Minute)}, false},
		{"one of several in the window", []time.Time{before(-time.Minute), before(2 * time.Hour), before(30 * time.Hour)}, true},
	}

	for _, tt := range tests {
		cfg := RuleConfig{Limit: 24}
		device := Input{EventTime: purchaseTime, AccountHistory: &AccountHistory{NewDeviceLogins: tt.logins}}
		country := Input{EventTime: purchaseTime, AccountHistory: &AccountHistory{NewCountryLogins: tt.logins}}

		if got := newDeviceLogin(device, cfg); got != tt.want {
			t.Errorf("newDeviceLogin(%s) = %v, want %v", tt.name, got, tt.want)
		}
		if got := newCountryLogin(country, cfg); got != tt.want {
			t.Errorf("newCountryLogin(%s) = %v, want %v", tt.name, got, tt.want)
		}
		if newCountryLogin(device, cfg) || newDeviceLogin(country, cfg) {
			t.Errorf("%s: a new device fired the country rule or a new country the device rule", tt.name)
		}
	}
}

func TestLoginRulesDoNotFireWithoutLoadedHistory(t *testing.T) {
	in := Input{EventTime: purchaseTime}
	cfg := RuleConfig{Limit: 24}
	if newDeviceLogin(in, cfg) || newCountryLogin(in, cfg) || recentPasswordReset(in, cfg) {
		t.Error("an account event rule fired without account history")
	}
}

func TestAccountEventLookbackIsTheLongestEnabledWindow(t *testing.T) {
	tests := []struct {
		name  string
		rules map[RuleName]RuleConfig
		want  time.Duration
	}{
		{"no rules", nil, 0},
		{"no account event rules", map[RuleName]RuleConfig{RuleNewAccount: {Enabled: true, Limit: 500}}, 0},
		{"disabled account event rule", map[RuleName]RuleConfig{RuleRecentPasswordReset: {Limit: 24}}, 0},
		{"longest enabled window", map[RuleName]RuleConfig{
			RuleRecentPasswordReset: {Enabled: true, Limit: 24},
			RuleNewDeviceLogin:      {Enabled: true, Limit: 72},
			RuleNewCountryLogin:     {Enabled: false, Limit: 168},
			RuleNewAccount:          {Enabled: true, Limit: 500},
		}, 72 * time.Hour},
	}

	for _, tt := range tests {
		if got := AccountEventLookback(Ruleset{Rules: tt.rules}); got != tt.want {
			t.Errorf("AccountEventLookback(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAccountEventRulesAreKnownAndUseAccountEvents(t *testing.T) {
	for _, name := range []RuleName{RuleRecentPasswordReset, RuleNewDeviceLogin, RuleNewCountryLogin} {
		if !name.Known() || !name.UsesAccountEvents() {
			t.Errorf("%s: Known() = %v, UsesAccountEvents() = %v, want both true", name, name.Known(), name.UsesAccountEvents())
		}
	}
	if RuleNewAccount.UsesAccountEvents() {
		t.Error("new_account must not read account events")
	}
}

func TestAccountEventRulesAreDisabledWhenMissingFromTheRuleset(t *testing.T) {
	reset := before(time.Hour)
	in := Input{EventTime: purchaseTime, AccountHistory: &AccountHistory{
		LastPasswordReset: &reset,
		NewDeviceLogins:   []time.Time{reset},
		NewCountryLogins:  []time.Time{reset},
	}}
	got := Evaluate(in, Ruleset{ReviewThreshold: 10, BlockThreshold: 100, Rules: map[RuleName]RuleConfig{}})
	if got.Score != 0 || len(got.FiredRules) != 0 {
		t.Errorf("Evaluate = %+v, want no fired rules", got)
	}
}

func TestAccountEventRuleMessagesStateTheAgeAndTheLimit(t *testing.T) {
	reset := before(90 * time.Minute)
	in := Input{EventTime: purchaseTime, AccountHistory: &AccountHistory{
		LastPasswordReset: &reset,
		NewDeviceLogins:   []time.Time{before(5 * time.Hour), before(30 * time.Hour)},
		NewCountryLogins:  []time.Time{before(time.Minute)},
	}}
	rs := Ruleset{ReviewThreshold: 10, BlockThreshold: 1000, Rules: map[RuleName]RuleConfig{
		RuleRecentPasswordReset: {Enabled: true, Weight: 30, Limit: 24},
		RuleNewDeviceLogin:      {Enabled: true, Weight: 25, Limit: 24},
		RuleNewCountryLogin:     {Enabled: true, Weight: 20, Limit: 1},
	}}

	got := Evaluate(in, rs)

	want := []FiredRule{
		{Name: RuleRecentPasswordReset, Points: 30, Message: "Password reset 1 hour before the purchase (limit 24 hours)"},
		{Name: RuleNewDeviceLogin, Points: 25, Message: "Login from a device the player had not used before, 5 hours before the purchase (limit 24 hours)"},
		{Name: RuleNewCountryLogin, Points: 20, Message: "Login from a country the player had not used before, 1 minute before the purchase (limit 1 hour)"},
	}
	if !reflect.DeepEqual(got.FiredRules, want) {
		t.Errorf("fired = %+v, want %+v", got.FiredRules, want)
	}
}
