package lgbm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// Scorer gives a fraud score from 0 to 1 for a feature vector. *Model and
// *Combined implement it.
type Scorer interface {
	Predict(x []float64) (float64, error)
	Features() []string
	Version() string
	Trees() int
}

// percentileTolerance must match PERCENTILE_TOLERANCE in ml/export.py: a table
// value counts as below a score only when it is lower by more than this, so a
// score computed one bit apart from Python lands on the same percentile.
const percentileTolerance = 1e-12

// Combined scores a purchase with several models and keeps the highest
// percentile. A model's percentile is the share of its validation scores that
// are below the purchase's score, so different models share one scale.
type Combined struct {
	version  string
	features []string
	models   []*Model
	tables   [][]float64
}

type rawCombined struct {
	Version      string   `json:"model_version"`
	Combination  string   `json:"combination"`
	FeatureNames []string `json:"feature_names"`
	Models       []struct {
		File        string    `json:"file"`
		Percentiles []float64 `json:"percentiles"`
	} `json:"models"`
}

// LoadCombined reads a combination written by ml/export.py. The model files it
// names are loaded from the same folder.
func LoadCombined(path string) (*Combined, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read combined model %s: %w", path, err)
	}
	var raw rawCombined
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse combined model %s: %w", path, err)
	}
	if raw.Combination != "max_percentile" {
		return nil, fmt.Errorf("combined model %s has combination %q, want max_percentile", path, raw.Combination)
	}
	if raw.Version == "" || len(raw.FeatureNames) == 0 || len(raw.Models) == 0 {
		return nil, fmt.Errorf("combined model %s needs model_version, feature_names and models", path)
	}

	c := &Combined{version: raw.Version, features: raw.FeatureNames}
	for _, part := range raw.Models {
		if len(part.Percentiles) == 0 || !sort.Float64sAreSorted(part.Percentiles) {
			return nil, fmt.Errorf("combined model %s: percentiles of %s must be sorted and not empty", path, part.File)
		}
		if part.File == "" || filepath.Base(part.File) != part.File {
			return nil, fmt.Errorf("combined model %s: model file %q must be a file name in the same folder", path, part.File)
		}
		model, err := Load(filepath.Join(filepath.Dir(path), part.File))
		if err != nil {
			return nil, fmt.Errorf("combined model %s: %w", path, err)
		}
		if !slices.Equal(model.Features(), raw.FeatureNames) {
			return nil, fmt.Errorf("combined model %s: %s has different features", path, part.File)
		}
		c.models = append(c.models, model)
		c.tables = append(c.tables, part.Percentiles)
	}
	return c, nil
}

// Predict returns the highest percentile among the models.
func (c *Combined) Predict(x []float64) (float64, error) {
	best := 0.0
	for i, model := range c.models {
		score, err := model.Predict(x)
		if err != nil {
			return 0, err
		}
		table := c.tables[i]
		below := sort.SearchFloat64s(table, score-percentileTolerance)
		best = max(best, float64(below)/float64(len(table)))
	}
	return best, nil
}

// Version returns the combination's version, such as "lgbm-ieee-v4-combined".
func (c *Combined) Version() string { return c.version }

// Features returns the feature names in the order Predict expects.
func (c *Combined) Features() []string { return append([]string(nil), c.features...) }

// Trees returns the number of trees in all models together.
func (c *Combined) Trees() int {
	total := 0
	for _, model := range c.models {
		total += model.Trees()
	}
	return total
}

// LoadScorer loads a single model or a combination, depending on the file.
func LoadScorer(path string) (Scorer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read model %s: %w", path, err)
	}
	var probe struct {
		Combination string `json:"combination"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse model %s: %w", path, err)
	}
	if probe.Combination != "" {
		return LoadCombined(path)
	}
	model, err := Load(path)
	if err != nil {
		return nil, err
	}
	return model, nil
}
