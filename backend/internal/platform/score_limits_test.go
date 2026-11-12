package platform

import (
	"testing"
	"time"
)

func requiredConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "test-key")
	for _, name := range []string{"SCORE_RATE_LIMIT", "SCORE_RATE_BURST", "SCORE_TIMEOUT"} {
		t.Setenv(name, "")
	}
}

func TestScoreLimitsDefaults(t *testing.T) {
	requiredConfig(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := ScoreLimits{RatePerSecond: 100, Burst: 200, Timeout: 2 * time.Second}
	if cfg.ScoreLimits != want {
		t.Fatalf("ScoreLimits = %+v, want %+v", cfg.ScoreLimits, want)
	}
}

func TestScoreLimitsFromEnvironment(t *testing.T) {
	requiredConfig(t)
	t.Setenv("SCORE_RATE_LIMIT", "12.5")
	t.Setenv("SCORE_RATE_BURST", "30")
	t.Setenv("SCORE_TIMEOUT", "750ms")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := ScoreLimits{RatePerSecond: 12.5, Burst: 30, Timeout: 750 * time.Millisecond}
	if cfg.ScoreLimits != want {
		t.Fatalf("ScoreLimits = %+v, want %+v", cfg.ScoreLimits, want)
	}

	t.Setenv("SCORE_RATE_LIMIT", "0")
	if cfg, err = LoadConfig(); err != nil || cfg.ScoreLimits.RatePerSecond != 0 {
		t.Fatalf("SCORE_RATE_LIMIT=0 gave %+v, %v; want the limit turned off", cfg.ScoreLimits, err)
	}
}

func TestScoreLimitsRejectBadValues(t *testing.T) {
	tests := []struct{ name, value string }{
		{"SCORE_RATE_LIMIT", "-1"},
		{"SCORE_RATE_LIMIT", "fast"},
		{"SCORE_RATE_LIMIT", "NaN"},
		{"SCORE_RATE_LIMIT", "Inf"},
		{"SCORE_RATE_BURST", "0"},
		{"SCORE_RATE_BURST", "1.5"},
		{"SCORE_TIMEOUT", "0s"},
		{"SCORE_TIMEOUT", "2"},
		{"SCORE_TIMEOUT", "-1s"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"="+tt.value, func(t *testing.T) {
			requiredConfig(t)
			t.Setenv(tt.name, tt.value)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("%s=%s was accepted", tt.name, tt.value)
			}
		})
	}
}
