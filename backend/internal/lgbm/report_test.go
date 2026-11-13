package lgbm

import (
	"path/filepath"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

func TestLoadReportUsesMetricsFromTheLoadedModel(t *testing.T) {
	cases := []struct {
		version string
		want    models.ModelEvaluation
	}{
		{"lgbm-game-v9", models.ModelEvaluation{Dataset: "Held-out simulator traffic", ROCAUC: 0.9926, PRAUC: 0.9624}},
		{"lgbm-game-v5", models.ModelEvaluation{Dataset: "Simulated game traffic test split", ROCAUC: 0.9965, PRAUC: 0.9691}},
		{"lgbm-ieee-v5", models.ModelEvaluation{Dataset: "IEEE-CIS test month", ROCAUC: 0.8508, PRAUC: 0.2469}},
		{"lgbm-ieee-v5-combined", models.ModelEvaluation{Dataset: "IEEE-CIS test month", ROCAUC: 0.8411, PRAUC: 0.2404}},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			path := filepath.Join("../../models", tc.version+".report.json")
			_, evaluation, err := LoadReport(path, tc.version)
			if err != nil || evaluation == nil || *evaluation != tc.want {
				t.Fatalf("LoadReport evaluation = %+v, err = %v, want %+v", evaluation, err, tc.want)
			}
		})
	}
}

func TestLoadReportIgnoresMetricsFromAnotherVersion(t *testing.T) {
	policy, evaluation, err := LoadReport(ReportPath(modelPath), "another-version")
	if err != nil || evaluation != nil || policy.FlagAt != 0.544632 {
		t.Fatalf("LoadReport = %+v, %+v, %v", policy, evaluation, err)
	}
}
