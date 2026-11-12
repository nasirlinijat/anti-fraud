package platform

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestDBMaxConns(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
	t.Setenv("API_KEY", "test-key")

	for _, tt := range []struct {
		raw  string
		want int32
	}{{"", 0}, {"  ", 0}, {"1", 1}, {"20", 20}, {" 12 ", 12}} {
		t.Setenv("DB_MAX_CONNS", tt.raw)
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("DB_MAX_CONNS=%q: %v", tt.raw, err)
		}
		if cfg.DBMaxConns != tt.want {
			t.Errorf("DB_MAX_CONNS=%q gives %d, want %d", tt.raw, cfg.DBMaxConns, tt.want)
		}
	}

	for _, raw := range []string{"0", "-1", "ten", "2.5", "2147483648"} {
		t.Setenv("DB_MAX_CONNS", raw)
		if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "DB_MAX_CONNS must be a whole number") {
			t.Errorf("DB_MAX_CONNS=%q: error %v, want a clear DB_MAX_CONNS error", raw, err)
		}
	}
}

func TestNewPoolSize(t *testing.T) {
	const url = "postgres://user:pass@127.0.0.1:1/none?connect_timeout=1"
	defaultSize := int32(max(4, runtime.NumCPU()))

	for _, tt := range []struct {
		name     string
		url      string
		maxConns int32
		want     int32
	}{
		{"pgx default", url, 0, defaultSize},
		{"DB_MAX_CONNS", url, 12, 12},
		{"pool_max_conns in the URL without DB_MAX_CONNS", url + "&pool_max_conns=7", 0, 7},
		{"DB_MAX_CONNS wins over the URL", url + "&pool_max_conns=7", 3, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewPool(context.Background(), tt.url, tt.maxConns)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if got := pool.Config().MaxConns; got != tt.want {
				t.Errorf("MaxConns = %d, want %d", got, tt.want)
			}
		})
	}
}
