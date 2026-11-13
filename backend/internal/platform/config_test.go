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

	t.Setenv("MODEL_PATH", "models/lgbm-game-v9.json")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ModelPath != "models/lgbm-game-v9.json" {
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

func TestLoadConfigRejectsWhitespaceOrControlCharsInAPIKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	for name, key := range map[string]string{
		"leading space":   " secret-key",
		"inner space":     "secret key",
		"newline":         "secret-key\n",
		"tab":             "secret\tkey",
		"carriage return": "secret\rkey",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("API_KEY", key)
			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), "API_KEY") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error for %s: %v", name, err)
			}
		})
	}
}

func TestLoadConfigRedisURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "test-key")

	t.Run("optional when empty", func(t *testing.T) {
		t.Setenv("REDIS_URL", "")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.RedisURL != "" {
			t.Errorf("RedisURL = %q, want empty", cfg.RedisURL)
		}
	})

	t.Run("valid url", func(t *testing.T) {
		t.Setenv("REDIS_URL", "redis://localhost:6379/0")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.RedisURL != "redis://localhost:6379/0" {
			t.Errorf("RedisURL = %q, want redis://localhost:6379/0", cfg.RedisURL)
		}
	})

	t.Run("invalid url hides secret", func(t *testing.T) {
		t.Setenv("REDIS_URL", "not-a-redis-url://secret-password@invalid")
		_, err := LoadConfig()
		if err == nil {
			t.Fatal("LoadConfig returned no error for invalid REDIS_URL")
		}
		if !strings.Contains(err.Error(), "REDIS_URL") {
			t.Errorf("error %q does not name REDIS_URL", err)
		}
		if strings.Contains(err.Error(), "secret-password") {
			t.Errorf("error %q leaks credentials", err)
		}
	})
}

func TestNewRedis(t *testing.T) {
	client, err := NewRedis("redis://localhost:6379/0")
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	defer client.Close()

	if _, err := NewRedis("invalid-scheme://localhost"); err == nil {
		t.Error("NewRedis returned no error for invalid URL")
	}
}
