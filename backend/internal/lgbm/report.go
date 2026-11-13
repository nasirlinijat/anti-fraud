package lgbm

import (
	"encoding/json"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// LoadReport reads the model policy and any evaluation metrics for version.
func LoadReport(path, version string) (scoring.ModelPolicy, *models.ModelEvaluation, error) {
	data, policy, err := readReport(path)
	if err != nil {
		return scoring.ModelPolicy{}, nil, err
	}
	return policy, readEvaluation(data, version), nil
}

type rankingMetrics struct {
	ROCAUC *float64 `json:"roc_auc"`
	PRAUC  *float64 `json:"pr_auc"`
}

func (m rankingMetrics) evaluation(dataset string) *models.ModelEvaluation {
	if m.ROCAUC == nil || m.PRAUC == nil || *m.ROCAUC < 0 || *m.ROCAUC > 1 || *m.PRAUC < 0 || *m.PRAUC > 1 {
		return nil
	}
	return &models.ModelEvaluation{Dataset: dataset, ROCAUC: *m.ROCAUC, PRAUC: *m.PRAUC}
}

func readEvaluation(data []byte, version string) *models.ModelEvaluation {
	var report struct {
		Export struct {
			Version string `json:"version"`
		} `json:"export"`
		Data        string         `json:"data"`
		Combination string         `json:"combination"`
		Test        rankingMetrics `json:"test"`
		LightGBM    struct {
			Test rankingMetrics `json:"test"`
		} `json:"lightgbm"`
		Development []struct {
			Models map[string]struct {
				AllDays rankingMetrics `json:"all_days"`
			} `json:"models"`
		} `json:"development"`
	}
	if err := json.Unmarshal(data, &report); err != nil || report.Export.Version != version {
		return nil
	}
	for _, run := range report.Development {
		if evaluation := run.Models[version].AllDays.evaluation("Held-out simulator traffic"); evaluation != nil {
			return evaluation
		}
	}
	switch {
	case report.Data == "simulated game traffic":
		return report.LightGBM.Test.evaluation("Simulated game traffic test split")
	case report.Data == "IEEE-CIS":
		return report.LightGBM.Test.evaluation("IEEE-CIS test month")
	case report.Combination != "":
		return report.Test.evaluation("IEEE-CIS test month")
	default:
		return nil
	}
}
