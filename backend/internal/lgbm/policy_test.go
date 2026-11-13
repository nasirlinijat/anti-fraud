package lgbm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func TestReportPath(t *testing.T) {
	if got := ReportPath("models/lgbm-game-v9.json"); got != "models/lgbm-game-v9.report.json" {
		t.Errorf("ReportPath = %q", got)
	}
}

func TestLoadPolicyFromExportedReport(t *testing.T) {
	p, err := LoadPolicy(ReportPath(modelPath))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	want := scoring.ModelPolicy{FlagAt: 0.544632, ClearBelow: 0.011527}
	if p != want {
		t.Errorf("policy = %+v, want %+v", p, want)
	}
}

func TestLoadPolicyRejectsBadReports(t *testing.T) {
	reports := map[string]string{
		"not json":       `{`,
		"no policy":      `{"features": []}`,
		"no clear_below": `{"policy": {"flag_at": 0.3}}`,
		"wrong order":    `{"policy": {"flag_at": 0.1, "clear_below": 0.2}}`,
	}
	dir := t.TempDir()
	for name, body := range reports {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, "report.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPolicy(path); err == nil {
				t.Error("LoadPolicy accepted a bad report")
			}
		})
	}
	if _, err := LoadPolicy(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("LoadPolicy accepted a missing file")
	}
}
