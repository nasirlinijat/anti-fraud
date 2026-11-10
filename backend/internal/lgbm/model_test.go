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

const (
	modelPath   = "../../models/lgbm-ieee-v3.json"
	goldenRows  = 2000
	maxScoreGap = 1e-9
)

const tinyModel = `{
  "model_version": "tiny-v1",
  "objective": "binary sigmoid:1",
  "feature_names": ["a", "b"],
  "tree_info": [
    {"tree_structure": {
      "split_feature": 0, "threshold": 0.5, "decision_type": "<=", "missing_type": "None",
      "left_child": {"leaf_value": -1.0},
      "right_child": {
        "split_feature": 1, "threshold": 10, "decision_type": "<=", "missing_type": "None",
        "left_child": {"leaf_value": 0.5},
        "right_child": {"leaf_value": 2.0}
      }
    }},
    {"tree_structure": {"leaf_value": 0.25}}
  ]
}`

func writeModel(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write model: %v", err)
	}
	return path
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

func TestPredictTinyModel(t *testing.T) {
	model, err := Load(writeModel(t, tinyModel))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cases := []struct {
		x    []float64
		want float64
	}{
		{[]float64{0.5, 100}, sigmoid(-1.0 + 0.25)},
		{[]float64{0.6, 10}, sigmoid(0.5 + 0.25)},
		{[]float64{0.6, 11}, sigmoid(2.0 + 0.25)},
	}
	for _, c := range cases {
		got, err := model.Predict(c.x)
		if err != nil {
			t.Fatalf("Predict(%v): %v", c.x, err)
		}
		if math.Abs(got-c.want) > 1e-15 {
			t.Errorf("Predict(%v) = %v, want %v", c.x, got, c.want)
		}
	}
	if model.Version() != "tiny-v1" || model.Trees() != 2 || !slices.Equal(model.Features(), []string{"a", "b"}) {
		t.Errorf("metadata = %q, %d trees, %v", model.Version(), model.Trees(), model.Features())
	}
}

// TestPredictMatchesGoldenRows checks every exported model against the scores
// Python computed for its golden rows.
func TestPredictMatchesGoldenRows(t *testing.T) {
	for _, version := range []string{"lgbm-ieee-v3", "lgbm-ieee-v4"} {
		t.Run(version, func(t *testing.T) {
			checkGoldenRows(t, filepath.Join("../../models", version+".json"), filepath.Join("../../models", version+".golden.csv"))
		})
	}
}

func checkGoldenRows(t *testing.T, modelPath, goldenPath string) {
	t.Helper()
	model, err := Load(modelPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	file, err := os.Open(goldenPath)
	if err != nil {
		t.Fatalf("open golden rows: %v", err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read golden rows: %v", err)
	}

	header := records[0]
	if header[len(header)-1] != "expected" {
		t.Fatalf("last golden column is %q, want expected", header[len(header)-1])
	}
	if !slices.Equal(header[:len(header)-1], model.Features()) {
		t.Fatalf("golden columns %v do not match model features %v", header[:len(header)-1], model.Features())
	}
	if len(records)-1 != goldenRows {
		t.Fatalf("got %d golden rows, want %d", len(records)-1, goldenRows)
	}

	worst := 0.0
	for line, record := range records[1:] {
		values := make([]float64, len(record))
		for i, field := range record {
			values[i], err = strconv.ParseFloat(field, 64)
			if err != nil {
				t.Fatalf("row %d column %d: %v", line+1, i, err)
			}
		}
		features, expected := values[:len(values)-1], values[len(values)-1]
		got, err := model.Predict(features)
		if err != nil {
			t.Fatalf("row %d: %v", line+1, err)
		}
		worst = max(worst, math.Abs(got-expected))
	}
	if worst > maxScoreGap {
		t.Errorf("largest score difference from Python is %g, want at most %g", worst, maxScoreGap)
	}
}

func TestLoadRejectsBadModels(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"invalid json", "{", "parse model"},
		{"no version", strings.Replace(tinyModel, `"model_version": "tiny-v1",`, "", 1), "no model_version"},
		{"regression objective", strings.Replace(tinyModel, "binary sigmoid:1", "regression", 1), "want binary"},
		{"categorical split", strings.Replace(tinyModel, `"threshold": 0.5, "decision_type": "<="`, `"threshold": 0.5, "decision_type": "=="`, 1), "unsupported decision type"},
		{"missing value handling", strings.Replace(tinyModel, `"threshold": 10, "decision_type": "<=", "missing_type": "None"`, `"threshold": 10, "decision_type": "<=", "missing_type": "NaN"`, 1), "unsupported missing type"},
		{"feature out of range", strings.Replace(tinyModel, `"split_feature": 1`, `"split_feature": 5`, 1), "out of range"},
		{"no trees", strings.Replace(tinyModel, `"tree_info": [`, `"tree_info": [], "unused": [`, 1), "no trees"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeModel(t, c.content))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("Load error = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func TestLoadMissingFileNamesThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("Load error = %v, want it to name %s", err, path)
	}
}

func TestPredictRejectsWrongFeatureCount(t *testing.T) {
	model, err := Load(writeModel(t, tinyModel))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := model.Predict([]float64{1}); err == nil {
		t.Error("Predict accepted 1 value for a 2-feature model")
	}
}

func BenchmarkPredict(b *testing.B) {
	model, err := Load(modelPath)
	if err != nil {
		b.Fatalf("Load: %v", err)
	}
	x := make([]float64, len(model.Features()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := model.Predict(x); err != nil {
			b.Fatal(err)
		}
	}
}
