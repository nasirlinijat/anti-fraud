package platform

import (
	"strings"
	"testing"
)

func TestLoadAnalystKeys(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "service-key")
	t.Setenv("REVIEW_ANALYST_KEYS", `[{"analyst_id":"alice","key":"alice-key"},{"analyst_id":"bob","key":"bob-key"}]`)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"alice-key": "alice", "bob-key": "bob", "service-key": "", "alice": ""} {
		if got := cfg.AnalystKeys.Analyst(key); got != want {
			t.Errorf("analyst = %q, want %q", got, want)
		}
	}
}

func TestParseAnalystKeysRejectsInvalidConfiguration(t *testing.T) {
	for name, value := range map[string]string{
		"not an array":  `null`,
		"unknown field": `[{"analyst_id":"alice","key":"alice-key","extra":true}]`,
		"empty id":      `[{"analyst_id":" ","key":"alice-key"}]`,
		"duplicate id":  `[{"analyst_id":"alice","key":"alice-key"},{"analyst_id":"alice","key":"bob-key"}]`,
		"duplicate key": `[{"analyst_id":"alice","key":"alice-key"},{"analyst_id":"bob","key":"alice-key"}]`,
		"service key":   `[{"analyst_id":"alice","key":"service-key"}]`,
		"trailing JSON": `[] []`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseAnalystKeys(value, "service-key")
			if err == nil || !strings.Contains(err.Error(), "REVIEW_ANALYST_KEYS") ||
				strings.Contains(err.Error(), "alice-key") || strings.Contains(err.Error(), "service-key") {
				t.Fatalf("unexpected configuration error: %v", err)
			}
		})
	}
}

func TestOperatorKeyMustDifferFromAnalystAndServiceKeys(t *testing.T) {
	analysts, err := ParseAnalystKeys(`[{"analyst_id":"alice","key":"analyst-key"}]`, "service-key")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"analyst-key", "service-key"} {
		_, err := ParseOperatorKeys(`[{"operator_id":"alice","key":"`+key+`"}]`, "service-key", analysts)
		if err == nil || strings.Contains(err.Error(), key) {
			t.Fatalf("unexpected operator key configuration error: %v", err)
		}
	}
}
