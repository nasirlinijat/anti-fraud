package platform

import "testing"

func TestMetricsAddr(t *testing.T) {
	requiredConfig(t)
	t.Setenv("DOGSTATSD_ADDR", "")
	cfg, err := LoadConfig()
	if err != nil || cfg.MetricsAddr != "" {
		t.Fatalf("MetricsAddr = %q, %v; want empty, so metrics are off", cfg.MetricsAddr, err)
	}

	t.Setenv("DOGSTATSD_ADDR", " datadog:8125 ")
	if cfg, err = LoadConfig(); err != nil || cfg.MetricsAddr != "datadog:8125" {
		t.Fatalf("MetricsAddr = %q, %v; want datadog:8125", cfg.MetricsAddr, err)
	}

	for _, bad := range []string{"datadog", "datadog:", ":::"} {
		t.Setenv("DOGSTATSD_ADDR", bad)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("DOGSTATSD_ADDR=%q was accepted", bad)
		}
	}
}
