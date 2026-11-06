package scoring

import (
	"testing"
	"time"
)

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
