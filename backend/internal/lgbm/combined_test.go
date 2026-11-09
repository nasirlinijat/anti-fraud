package lgbm

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const combinedPath = "../../models/lgbm-ieee-v4-combined.json"

func TestCombinedMatchesGoldenRows(t *testing.T) {
	scorer, err := LoadScorer(combinedPath)
	if err != nil {
		t.Fatalf("LoadScorer: %v", err)
	}
	combined, ok := scorer.(*Combined)
	if !ok {
		t.Fatalf("LoadScorer returned %T, want *Combined", scorer)
	}
	if combined.Version() != "lgbm-ieee-v4-combined" || combined.Trees() < 2 {
		t.Errorf("version %q, trees %d", combined.Version(), combined.Trees())
	}

	file, err := os.Open("../../models/lgbm-ieee-v4-combined.golden.csv")
	if err != nil {
		t.Fatalf("open golden rows: %v", err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read golden rows: %v", err)
	}
	header := records[0]
	if !slices.Equal(header[:len(header)-1], combined.Features()) {
		t.Fatalf("golden columns do not match the combined model features")
	}
	worst := 0.0
	for line, record := range records[1:] {
		values := make([]float64, len(record))
		for i, field := range record {
			if values[i], err = strconv.ParseFloat(field, 64); err != nil {
				t.Fatalf("row %d column %d: %v", line+1, i, err)
			}
		}
		got, err := combined.Predict(values[:len(values)-1])
		if err != nil {
			t.Fatalf("row %d: %v", line+1, err)
		}
		worst = max(worst, math.Abs(got-values[len(values)-1]))
	}
	if worst > 1e-12 {
		t.Errorf("largest combined score difference from Python is %g", worst)
	}
}

func TestLoadScorerLoadsSingleModels(t *testing.T) {
	scorer, err := LoadScorer(modelPath)
	if err != nil {
		t.Fatalf("LoadScorer: %v", err)
	}
	if _, ok := scorer.(*Model); !ok {
		t.Fatalf("LoadScorer returned %T, want *Model", scorer)
	}
}

// writeCombined writes a combination of the tiny model into a temporary folder.
func writeCombined(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tiny.json"), []byte(tinyModel), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "combined.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCombinedTakesTheHighestPercentile(t *testing.T) {
	path := writeCombined(t, `{"model_version": "c1", "combination": "max_percentile", "feature_names": ["a", "b"],
		"models": [
			{"file": "tiny.json", "percentiles": [0.1, 0.2, 0.3, 0.9]},
			{"file": "tiny.json", "percentiles": [0.5, 0.6, 0.7, 0.8]}
		]}`)
	c, err := LoadCombined(path)
	if err != nil {
		t.Fatalf("LoadCombined: %v", err)
	}
	raw, _ := c.models[0].Predict([]float64{1, 20})
	got, err := c.Predict([]float64{1, 20})
	if err != nil {
		t.Fatal(err)
	}
	below := func(table []float64) float64 {
		n := 0
		for _, v := range table {
			if v < raw-percentileTolerance {
				n++
			}
		}
		return float64(n) / float64(len(table))
	}
	want := max(below(c.tables[0]), below(c.tables[1]))
	if got != want {
		t.Errorf("combined score %v for raw score %v, want %v", got, raw, want)
	}
}

func TestLoadCombinedRejectsBadFiles(t *testing.T) {
	cases := map[string]string{
		"other combination": `{"model_version": "c1", "combination": "average", "feature_names": ["a", "b"], "models": [{"file": "tiny.json", "percentiles": [0.1]}]}`,
		"no models":         `{"model_version": "c1", "combination": "max_percentile", "feature_names": ["a", "b"], "models": []}`,
		"unsorted table":    `{"model_version": "c1", "combination": "max_percentile", "feature_names": ["a", "b"], "models": [{"file": "tiny.json", "percentiles": [0.5, 0.1]}]}`,
		"missing file":      `{"model_version": "c1", "combination": "max_percentile", "feature_names": ["a", "b"], "models": [{"file": "none.json", "percentiles": [0.1]}]}`,
		"file outside":      `{"model_version": "c1", "combination": "max_percentile", "feature_names": ["a", "b"], "models": [{"file": "../tiny.json", "percentiles": [0.1]}]}`,
		"other features":    `{"model_version": "c1", "combination": "max_percentile", "feature_names": ["a", "c"], "models": [{"file": "tiny.json", "percentiles": [0.1]}]}`,
		"not json":          `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadCombined(writeCombined(t, body)); err == nil {
				t.Error("LoadCombined accepted a bad file")
			}
		})
	}
	if _, err := LoadScorer(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "read model") {
		t.Errorf("LoadScorer on a missing file = %v", err)
	}
}
