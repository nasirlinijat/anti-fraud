package platform

import (
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "test-key")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want 9090", cfg.Port)
	}
	if cfg.DatabaseURL != "postgres://localhost/antifraud" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want test-key", cfg.APIKey)
	}
}

func TestLoadConfigDefaultsPort(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "test-key")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Port = %q, want %q", cfg.Port, defaultPort)
	}
}

func TestLoadConfigModelPathIsOptional(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "test-key")
	t.Setenv("MODEL_PATH", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ModelPath != "" {
		t.Errorf("ModelPath = %q, want empty", cfg.ModelPath)
	}

	t.Setenv("MODEL_PATH", "models/lgbm-ieee-v2.json")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ModelPath != "models/lgbm-ieee-v2.json" {
		t.Errorf("ModelPath = %q", cfg.ModelPath)
	}
}

func TestLoadConfigMissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("API_KEY", "test-key")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig returned no error")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error %q does not name DATABASE_URL", err)
	}
}

func TestLoadConfigMissingAPIKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig returned no error")
	}
	if !strings.Contains(err.Error(), "API_KEY") {
		t.Errorf("error %q does not name API_KEY", err)
	}
}
