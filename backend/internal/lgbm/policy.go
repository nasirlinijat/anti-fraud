package lgbm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// ReportPath returns the training report written next to a model file:
// models/lgbm-game-v9.json has models/lgbm-game-v9.report.json.
func ReportPath(modelPath string) string {
	return strings.TrimSuffix(modelPath, ".json") + ".report.json"
}

// LoadPolicy reads the model thresholds from the policy section of a training
// report written by ml/train.py.
func LoadPolicy(reportPath string) (scoring.ModelPolicy, error) {
	data, err := os.ReadFile(filepath.Clean(reportPath))
	if err != nil {
		return scoring.ModelPolicy{}, fmt.Errorf("read model report %s: %w", reportPath, err)
	}
	var report struct {
		Policy *struct {
			FlagAt     *float64 `json:"flag_at"`
			ClearBelow *float64 `json:"clear_below"`
			// Older reports have no blend weight; it is then 0.
			PlayerBlendWeight float64 `json:"player_blend_weight"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return scoring.ModelPolicy{}, fmt.Errorf("parse model report %s: %w", reportPath, err)
	}
	if report.Policy == nil || report.Policy.FlagAt == nil || report.Policy.ClearBelow == nil {
		return scoring.ModelPolicy{}, fmt.Errorf("model report %s has no policy with flag_at and clear_below", reportPath)
	}
	p := scoring.ModelPolicy{
		FlagAt:            *report.Policy.FlagAt,
		ClearBelow:        *report.Policy.ClearBelow,
		PlayerBlendWeight: report.Policy.PlayerBlendWeight,
	}
	if err := p.Validate(); err != nil {
		return scoring.ModelPolicy{}, fmt.Errorf("model report %s: %w", reportPath, err)
	}
	return p, nil
}
