package lgbm

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const explainModel = `{
  "model_version": "explain-test",
  "objective": "binary sigmoid:1",
  "feature_names": ["amount", "card_uses_1h", "unused"],
  "tree_info": [
    {"tree_structure": {
      "split_feature": 0, "threshold": 100, "decision_type": "<=", "missing_type": "None", "internal_value": 0.1,
      "left_child": {"leaf_value": -0.4},
      "right_child": {
        "split_feature": 1, "threshold": 2, "decision_type": "<=", "missing_type": "None", "internal_value": 0.3,
        "left_child": {"leaf_value": 0.2},
        "right_child": {"leaf_value": 0.9}
      }
    }},
    {"tree_structure": {"leaf_value": -1.5}}
  ]
}`

func TestContributionsFollowTheDecisionPath(t *testing.T) {
	model, err := Load(writeModel(t, explainModel))
	if err != nil {
		t.Fatal(err)
	}
	base, contributions, err := model.Contributions([]float64{250, 5, 7})
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0.3 - 0.1, 0.9 - 0.3, 0}
	for i := range want {
		if math.Abs(contributions[i]-want[i]) > 1e-12 {
			t.Fatalf("contribution %d = %g, want %g", i, contributions[i], want[i])
		}
	}
	if math.Abs(base-(0.1-1.5)) > 1e-12 {
		t.Fatalf("base = %g, want %g", base, 0.1-1.5)
	}
	score, _ := model.Predict([]float64{250, 5, 7})
	if math.Abs(sigmoid(base+contributions[0]+contributions[1]+contributions[2])-score) > 1e-12 {
		t.Fatalf("base and contributions do not give the score %g", score)
	}
}

func TestExplainOrdersReasonsByStrengthAndSkipsUnusedFeatures(t *testing.T) {
	model, err := Load(writeModel(t, explainModel))
	if err != nil {
		t.Fatal(err)
	}
	reasons, err := model.Explain([]float64{250, 5, 7}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 2 || reasons[0].Feature != "card_uses_1h" || reasons[0].Value != 5 || reasons[1].Feature != "amount" {
		t.Fatalf("got %+v", reasons)
	}
	lower, err := model.Explain([]float64{50, 5, 7}, 5)
	if err != nil || len(lower) != 1 || lower[0].Feature != "amount" || lower[0].Effect >= 0 {
		t.Fatalf("a small amount should only lower the risk: %+v, %v", lower, err)
	}
	top, err := model.Explain([]float64{250, 5, 7}, 1)
	if err != nil || len(top) != 1 || top[0].Feature != "card_uses_1h" {
		t.Fatalf("top 1: %+v, %v", top, err)
	}
	if _, err := model.Explain([]float64{1}, 5); err == nil {
		t.Fatal("a vector of the wrong length was explained")
	}
}

func TestContributionsAddUpToTheRawScoreOfEveryGoldenRow(t *testing.T) {
	for _, version := range []string{"lgbm-ieee-v5", "lgbm-ieee-v5-first", "lgbm-game-v5", "lgbm-game-v9"} {
		t.Run(version, func(t *testing.T) {
			model, err := Load(filepath.Join("../../models", version+".json"))
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(filepath.Join("../../models", version+".golden.csv"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			records, err := csv.NewReader(file).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			worst := 0.0
			for line, record := range records[1:] {
				x := make([]float64, len(record)-1)
				for i := range x {
					if x[i], err = strconv.ParseFloat(record[i], 64); err != nil {
						t.Fatalf("row %d column %d: %v", line+1, i, err)
					}
				}
				base, contributions, err := model.Contributions(x)
				if err != nil {
					t.Fatal(err)
				}
				total := base
				for _, c := range contributions {
					total += c
				}
				worst = max(worst, math.Abs(total-rawScore(model, x)))
			}
			if worst > 1e-9 {
				t.Fatalf("base and contributions differ from the raw score by up to %g", worst)
			}
		})
	}
}

func TestCombinedExplainsWithItsMainModel(t *testing.T) {
	combined, err := LoadCombined("../../models/lgbm-ieee-v5-combined.json")
	if err != nil {
		t.Fatal(err)
	}
	x := make([]float64, len(combined.Features()))
	got, err := combined.Explain(x, 5)
	if err != nil {
		t.Fatal(err)
	}
	want, err := combined.models[0].Explain(x, 5)
	if err != nil || len(got) != len(want) {
		t.Fatalf("got %+v, want %+v (%v)", got, want, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reason %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func rawScore(m *Model, x []float64) float64 {
	sum := 0.0
	for _, tree := range m.trees {
		i := int32(0)
		for tree[i].feature >= 0 {
			if x[tree[i].feature] <= tree[i].value {
				i = tree[i].left
			} else {
				i = tree[i].right
			}
		}
		sum += tree[i].value
	}
	return sum
}
