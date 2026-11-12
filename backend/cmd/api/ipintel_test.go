package main

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	scoresvc "github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
)

func TestNewIPIntelLoadsTheRangesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ranges.json")
	if err := os.WriteFile(path, []byte(`[{"cidr":"10.0.0.0/8","kind":"tor"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	mock, err := newIPIntel(platform.IPIntelConfig{Provider: platform.IPIntelMock, MockRangesPath: path})

	if err != nil || mock == nil {
		t.Fatalf("newIPIntel = %v, %v; want a mock", mock, err)
	}
	tests := map[string]ipintel.Result{
		"10.1.2.3":   {Provider: ipintel.MockName, Available: true, Tor: true},
		"192.0.2.10": {Provider: ipintel.MockName, Available: true},
	}
	for addr, want := range tests {
		if got, err := mock.Lookup(context.Background(), netip.MustParseAddr(addr)); err != nil || got != want {
			t.Errorf("Lookup(%s) = %+v, %v; want %+v", addr, got, err, want)
		}
	}
}

func TestNewIPIntelUsesTheDefaultRangesOrNone(t *testing.T) {
	mock, err := newIPIntel(platform.IPIntelConfig{Provider: platform.IPIntelMock})
	if err != nil || mock == nil {
		t.Fatalf("newIPIntel = %v, %v; want a mock", mock, err)
	}
	got, err := mock.Lookup(context.Background(), netip.MustParseAddr("192.0.2.10"))
	if err != nil || !got.Hosting {
		t.Errorf("Lookup(192.0.2.10) = %+v, %v; want a hosting address", got, err)
	}
	if mock, err := newIPIntel(platform.IPIntelConfig{}); err != nil || mock != nil {
		t.Errorf("newIPIntel with the provider off = %v, %v; want nil", mock, err)
	}
}

func TestUseIPIntelFailsOnABadRangesFile(t *testing.T) {
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalid, []byte(`[{"cidr":"10.0.0.0/8","kind":"residential"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{invalid, filepath.Join(dir, "missing.json")} {
		service := scoresvc.NewService(nil, nil, nil)
		if err := useIPIntel(platform.IPIntelConfig{Provider: platform.IPIntelMock, MockRangesPath: path}, service); err == nil {
			t.Errorf("useIPIntel(%s) returned no error", filepath.Base(path))
		}
	}
}
