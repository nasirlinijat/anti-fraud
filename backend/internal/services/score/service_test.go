package score

import (
	"slices"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

var testPolicy = scoring.ModelPolicy{FlagAt: 0.3, ClearBelow: 0.01}

// fakeModel returns a fixed score and records the features it was given.
type fakeModel struct {
	score    float64
	err      error
	features []string
	got      []float64
}

func (m *fakeModel) Predict(x []float64) (float64, error) {
	m.got = x
	return m.score, m.err
}

const fakeModelVersion = "fake-model-v1"

func (m *fakeModel) Version() string { return fakeModelVersion }

func (m *fakeModel) Features() []string {
	if m.features != nil {
		return m.features
	}
	return features.Names
}

func TestUseModelRejectsMismatch(t *testing.T) {
	h := NewService(nil, nil, nil)
	reordered := append([]string{features.Names[1], features.Names[0]}, features.Names[2:]...)
	if err := h.UseModel(&fakeModel{features: reordered}, testPolicy); err == nil {
		t.Error("UseModel accepted a model with features in another order")
	}
	if err := h.UseModel(&fakeModel{features: features.Names[1:]}, testPolicy); err == nil {
		t.Error("UseModel accepted a model that skips the first feature")
	}
	if err := h.UseModel(&fakeModel{features: append(slices.Clone(features.Names), "unknown")}, testPolicy); err == nil {
		t.Error("UseModel accepted a model with a feature the service does not compute")
	}
	if err := h.UseModel(&fakeModel{features: []string{}}, testPolicy); err == nil {
		t.Error("UseModel accepted a model without features")
	}
	if err := h.UseModel(&fakeModel{}, scoring.ModelPolicy{FlagAt: 0.1, ClearBelow: 0.2}); err == nil {
		t.Error("UseModel accepted a bad policy")
	}
	if h.model != nil {
		t.Error("a rejected model was kept")
	}
}

func TestUseModelAcceptsExportedModels(t *testing.T) {
	for _, path := range []string{"../../../models/lgbm-ieee-v5.json", "../../../models/lgbm-ieee-v5-combined.json", "../../../models/lgbm-game-v5.json", "../../../models/lgbm-game-v9.json"} {
		t.Run(path, func(t *testing.T) { useExportedModel(t, path) })
	}
}

func useExportedModel(t *testing.T, path string) {
	t.Helper()
	model, err := lgbm.LoadScorer(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	policy, err := lgbm.LoadPolicy(lgbm.ReportPath(path))
	if err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}
	h := NewService(nil, nil, nil)
	if err := h.UseModel(model, policy); err != nil {
		t.Fatalf("UseModel: %v", err)
	}
}
