package platform

import (
	"strings"
	"testing"
)

func TestLoadConfigChargebackWebhookSecretIsOptional(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "test-key")
	t.Setenv("CHARGEBACK_WEBHOOK_SECRET", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ChargebackWebhookSecret != "" {
		t.Errorf("ChargebackWebhookSecret = %q, want empty", cfg.ChargebackWebhookSecret)
	}

	secret := strings.Repeat("s", 32)
	t.Setenv("CHARGEBACK_WEBHOOK_SECRET", secret)
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ChargebackWebhookSecret != secret {
		t.Errorf("ChargebackWebhookSecret = %q, want %q", cfg.ChargebackWebhookSecret, secret)
	}
}

func TestLoadConfigRejectsWeakChargebackWebhookSecrets(t *testing.T) {
	apiKey := strings.Repeat("k", 40)
	analystKey := strings.Repeat("a", 40)
	operatorKey := strings.Repeat("o", 40)
	for name, secret := range map[string]string{
		"too short":            strings.Repeat("s", 31),
		"whitespace":           strings.Repeat("s", 32) + " x",
		"control character":    strings.Repeat("s", 32) + "\x7f",
		"same as API key":      apiKey,
		"same as analyst key":  analystKey,
		"same as operator key": operatorKey,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
			t.Setenv("API_KEY", apiKey)
			t.Setenv("REVIEW_ANALYST_KEYS", `[{"analyst_id":"analyst-1","key":"`+analystKey+`"}]`)
			t.Setenv("OPERATOR_API_KEYS", `[{"operator_id":"operator-1","key":"`+operatorKey+`"}]`)
			t.Setenv("CHARGEBACK_WEBHOOK_SECRET", secret)

			_, err := LoadConfig()

			if err == nil || !strings.Contains(err.Error(), "CHARGEBACK_WEBHOOK_SECRET") {
				t.Fatalf("err = %v, want an error naming CHARGEBACK_WEBHOOK_SECRET", err)
			}
		})
	}
}
