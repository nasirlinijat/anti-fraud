package lgbm

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

// node is one entry of a flattened tree. A leaf has feature -1 and stores its
// value; a split node stores its threshold and the indexes of its children.
type node struct {
	feature     int32
	left, right int32
	value       float64
	internal    float64
}

// Model is a LightGBM binary classifier loaded from its JSON dump.
type Model struct {
	version  string
	features []string
	trees    [][]node
}

type rawNode struct {
	SplitFeature  *int32   `json:"split_feature"`
	Threshold     float64  `json:"threshold"`
	DecisionType  string   `json:"decision_type"`
	MissingType   string   `json:"missing_type"`
	InternalValue float64  `json:"internal_value"`
	LeftChild     *rawNode `json:"left_child"`
	RightChild    *rawNode `json:"right_child"`
	LeafValue     float64  `json:"leaf_value"`
}

type rawModel struct {
	Version      string   `json:"model_version"`
	Objective    string   `json:"objective"`
	FeatureNames []string `json:"feature_names"`
	TreeInfo     []struct {
		Tree rawNode `json:"tree_structure"`
	} `json:"tree_info"`
}

// Load reads a LightGBM JSON dump written by ml/export.py. It returns an error
// if the file is missing, is not valid JSON, or uses anything the scorer does
// not support: a non-binary objective, categorical splits or missing-value
// handling.
func Load(path string) (*Model, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read model %s: %w", path, err)
	}

	var raw rawModel
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse model %s: %w", path, err)
	}
	if raw.Version == "" {
		return nil, fmt.Errorf("model %s has no model_version", path)
	}
	if !strings.HasPrefix(raw.Objective, "binary") {
		return nil, fmt.Errorf("model %s has objective %q, want binary", path, raw.Objective)
	}
	if len(raw.FeatureNames) == 0 {
		return nil, fmt.Errorf("model %s has no feature names", path)
	}
	if len(raw.TreeInfo) == 0 {
		return nil, fmt.Errorf("model %s has no trees", path)
	}

	model := &Model{
		version:  raw.Version,
		features: raw.FeatureNames,
		trees:    make([][]node, 0, len(raw.TreeInfo)),
	}
	for i := range raw.TreeInfo {
		nodes, err := flatten(&raw.TreeInfo[i].Tree, len(raw.FeatureNames))
		if err != nil {
			return nil, fmt.Errorf("model %s tree %d: %w", path, i, err)
		}
		model.trees = append(model.trees, nodes)
	}
	return model, nil
}

// flatten turns a nested tree into a slice, so that prediction walks indexes
// instead of pointers.
func flatten(root *rawNode, featureCount int) ([]node, error) {
	var nodes []node
	var walk func(n *rawNode) (int32, error)
	walk = func(n *rawNode) (int32, error) {
		index := int32(len(nodes))
		if n.SplitFeature == nil {
			nodes = append(nodes, node{feature: -1, value: n.LeafValue})
			return index, nil
		}
		if n.DecisionType != "<=" {
			return 0, fmt.Errorf("unsupported decision type %q", n.DecisionType)
		}
		if n.MissingType != "" && n.MissingType != "None" {
			return 0, fmt.Errorf("unsupported missing type %q", n.MissingType)
		}
		feature := *n.SplitFeature
		if feature < 0 || int(feature) >= featureCount {
			return 0, fmt.Errorf("split feature %d is out of range", feature)
		}
		if n.LeftChild == nil || n.RightChild == nil {
			return 0, errors.New("split node is missing a child")
		}

		nodes = append(nodes, node{feature: feature, value: n.Threshold, internal: n.InternalValue})
		left, err := walk(n.LeftChild)
		if err != nil {
			return 0, err
		}
		right, err := walk(n.RightChild)
		if err != nil {
			return 0, err
		}
		nodes[index].left, nodes[index].right = left, right
		return index, nil
	}

	if _, err := walk(root); err != nil {
		return nil, err
	}
	return nodes, nil
}

// Predict returns the fraud risk from 0 to 1 for one feature vector. The values
// must be in the order returned by Features.
func (m *Model) Predict(x []float64) (float64, error) {
	if len(x) != len(m.features) {
		return 0, fmt.Errorf("got %d feature values, want %d", len(x), len(m.features))
	}
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
	return 1 / (1 + math.Exp(-sum)), nil
}

// Version returns the model version, such as "lgbm-game-v9".
func (m *Model) Version() string { return m.version }

// Features returns the feature names in the order Predict expects.
func (m *Model) Features() []string {
	return append([]string(nil), m.features...)
}

// Trees returns the number of trees in the model.
func (m *Model) Trees() int { return len(m.trees) }

func (m *Model) Contributions(x []float64) (float64, []float64, error) {
	if len(x) != len(m.features) {
		return 0, nil, fmt.Errorf("got %d feature values, want %d", len(x), len(m.features))
	}
	base := 0.0
	contributions := make([]float64, len(m.features))
	for _, tree := range m.trees {
		base += tree[0].output()
		i := int32(0)
		for tree[i].feature >= 0 {
			split := tree[i]
			if x[split.feature] <= split.value {
				i = split.left
			} else {
				i = split.right
			}
			contributions[split.feature] += tree[i].output() - split.internal
		}
	}
	return base, contributions, nil
}

func (n node) output() float64 {
	if n.feature < 0 {
		return n.value
	}
	return n.internal
}

func (m *Model) Explain(x []float64, top int) ([]models.ModelReason, error) {
	_, contributions, err := m.Contributions(x)
	if err != nil {
		return nil, err
	}
	reasons := make([]models.ModelReason, 0, len(contributions))
	for i, effect := range contributions {
		if math.Abs(effect) < 1e-9 {
			continue
		}
		reasons = append(reasons, models.ModelReason{Feature: m.features[i], Value: x[i], Effect: effect})
	}
	sort.SliceStable(reasons, func(a, b int) bool { return math.Abs(reasons[a].Effect) > math.Abs(reasons[b].Effect) })
	if len(reasons) > top {
		reasons = reasons[:top]
	}
	return reasons, nil
}
