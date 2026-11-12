package platform

import "testing"

func TestLoadConfigIPIntel(t *testing.T) {
	tests := []struct {
		name, provider, ranges string
		want                   IPIntelConfig
		wantErr                bool
	}{
		{"unset is off", "", "", IPIntelConfig{}, false},
		{"off", "off", "", IPIntelConfig{}, false},
		{"none", "none", "", IPIntelConfig{}, false},
		{"mock", "mock", "", IPIntelConfig{Provider: IPIntelMock}, false},
		{"mock with ranges", "mock", "/etc/ranges.json", IPIntelConfig{Provider: IPIntelMock, MockRangesPath: "/etc/ranges.json"}, false},
		{"unknown provider", "maxmind", "", IPIntelConfig{}, true},
		{"provider in capitals", "MOCK", "", IPIntelConfig{}, true},
		{"ranges without the mock", "off", "/etc/ranges.json", IPIntelConfig{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost/antifraud")
			t.Setenv("API_KEY", "test-key")
			t.Setenv("IP_INTEL_PROVIDER", tt.provider)
			t.Setenv("IP_INTEL_MOCK_RANGES", tt.ranges)

			cfg, err := LoadConfig()

			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadConfig error = %v, want error %v", err, tt.wantErr)
			}
			if err == nil && cfg.IPIntel != tt.want {
				t.Errorf("IPIntel = %+v, want %+v", cfg.IPIntel, tt.want)
			}
		})
	}
}
