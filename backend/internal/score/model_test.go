package score

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func serveModel(t *testing.T, m Model, b Blocklist, p Purchases, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(fakeRulesets{rs: testRuleset}, b, p)
	if err := h.UseModel(m, testPolicy); err != nil {
		t.Fatalf("UseModel: %v", err)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, testKey)
	req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(body))
	req.Header.Set("X-API-Key", testKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// reviewBody scores 50 with the test ruleset (high amount and new account),
// which is a review.
const reviewBody = `{
	"transaction_id": "txn-review",
	"event_time": "2026-11-08T10:00:00Z",
	"amount_minor": 60000,
	"player_id": "player-1",
	"card_token": "card-1",
	"account_created_at": "2026-11-08T09:00:00Z"
}`

func TestModelChangesOutcome(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		cardUses int
		score    float64
		want     scoring.Outcome
	}{
		{"allow stays below flag_at", minimalBody, 0, 0.2999, scoring.OutcomeAllow},
		{"allow becomes review at flag_at", minimalBody, 0, 0.3, scoring.OutcomeReview},
		{"review is cleared below clear_below", reviewBody, 0, 0.0099, scoring.OutcomeAllow},
		{"review stays at clear_below", reviewBody, 0, 0.01, scoring.OutcomeReview},
		{"review stays at a high score", reviewBody, 0, 0.99, scoring.OutcomeReview},
		{"rule block stays at score 0", reviewBody, 5, 0, scoring.OutcomeBlock},
		{"rule block stays at score 1", reviewBody, 5, 1, scoring.OutcomeBlock},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			purchases := &fakePurchases{cardUses: tt.cardUses}
			rec := serveModel(t, &fakeModel{score: tt.score}, &fakeBlocklist{}, purchases, tt.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			got := decodeJSON[scoreResponse](t, rec)
			if got.Outcome != tt.want {
				t.Errorf("outcome = %s, want %s", got.Outcome, tt.want)
			}
			if got.ModelScore == nil || *got.ModelScore != tt.score {
				t.Errorf("response model_score = %v, want %v", got.ModelScore, tt.score)
			}
			if got.ModelVersion == nil || *got.ModelVersion != fakeModelVersion {
				t.Errorf("response model_version = %v, want %s", got.ModelVersion, fakeModelVersion)
			}
			d := purchases.decisions[0]
			if d.Result.Outcome != tt.want || d.ModelScore == nil || *d.ModelScore != tt.score {
				t.Errorf("saved decision outcome %s, model score %v; want %s, %v", d.Result.Outcome, d.ModelScore, tt.want, tt.score)
			}
			if d.ModelVersion == nil || *d.ModelVersion != fakeModelVersion {
				t.Errorf("saved model version = %v, want %s", d.ModelVersion, fakeModelVersion)
			}
		})
	}
}

func TestModelNeverUnblocksBlocklistMatch(t *testing.T) {
	purchases := &fakePurchases{}
	rec := serveModel(t, &fakeModel{score: 0}, &fakeBlocklist{matched: true}, purchases, minimalBody)
	got := decodeJSON[scoreResponse](t, rec)
	if got.Outcome != scoring.OutcomeBlock {
		t.Errorf("outcome = %s, want block", got.Outcome)
	}
	if got.ModelScore == nil {
		t.Error("model score is missing for a blocked purchase")
	}
}

func TestModelGetsFeatureVector(t *testing.T) {
	m := &fakeModel{score: 0.1}
	serveModel(t, m, &fakeBlocklist{}, &fakePurchases{}, minimalBody)
	if len(m.got) != len(features.Names) {
		t.Fatalf("model got %d features, want %d", len(m.got), len(features.Names))
	}
	if m.got[0] != 19.99 {
		t.Errorf("amount = %v, want 19.99", m.got[0])
	}
}

func TestRulesOnlyWithoutModel(t *testing.T) {
	purchases := &fakePurchases{}
	rec := serve(t, fakeRulesets{rs: testRuleset}, purchases, testKey, reviewBody)
	if !strings.Contains(rec.Body.String(), `"model_score":null`) {
		t.Errorf("body %s, want model_score null", rec.Body.String())
	}
	got := decodeJSON[scoreResponse](t, rec)
	if got.Outcome != scoring.OutcomeReview {
		t.Errorf("outcome = %s, want review from the rules alone", got.Outcome)
	}
	if purchases.decisions[0].ModelScore != nil {
		t.Errorf("saved model score = %v, want none", *purchases.decisions[0].ModelScore)
	}
}

func TestModelError(t *testing.T) {
	purchases := &fakePurchases{}
	rec := serveModel(t, &fakeModel{err: errors.New("bad vector")}, &fakeBlocklist{}, purchases, minimalBody)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if len(purchases.saved) != 0 {
		t.Error("a purchase was saved after the model failed")
	}
}

func TestUseModelRejectsMismatch(t *testing.T) {
	h := NewHandler(fakeRulesets{}, &fakeBlocklist{}, &fakePurchases{})
	reordered := append([]string{features.Names[1], features.Names[0]}, features.Names[2:]...)
	if err := h.UseModel(&fakeModel{features: reordered}, testPolicy); err == nil {
		t.Error("UseModel accepted a model with features in another order")
	}
	if err := h.UseModel(&fakeModel{}, scoring.ModelPolicy{FlagAt: 0.1, ClearBelow: 0.2}); err == nil {
		t.Error("UseModel accepted a bad policy")
	}
	if h.model != nil {
		t.Error("a rejected model was kept")
	}
}

func TestUseModelAcceptsExportedModels(t *testing.T) {
	for _, path := range []string{"../../models/lgbm-ieee-v5.json", "../../models/lgbm-ieee-v5-combined.json"} {
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
	h := NewHandler(fakeRulesets{}, &fakeBlocklist{}, &fakePurchases{})
	if err := h.UseModel(model, policy); err != nil {
		t.Fatalf("UseModel: %v", err)
	}
}

func TestModelScoreIsBlendedWithEarlierScores(t *testing.T) {
	purchases := &fakePurchases{earlierSum: 1.6, earlierCount: 2}
	h := NewHandler(fakeRulesets{rs: testRuleset}, &fakeBlocklist{}, purchases)
	policy := scoring.ModelPolicy{FlagAt: 0.4, ClearBelow: 0.01, PlayerBlendWeight: 0.5}
	if err := h.UseModel(&fakeModel{score: 0.2}, policy); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, testKey)
	req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(minimalBody))
	req.Header.Set("X-API-Key", testKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	got := decodeJSON[scoreResponse](t, rec)
	want := 0.5*0.2 + 0.5*0.8
	if got.ModelScore == nil || *got.ModelScore != want {
		t.Fatalf("model_score = %v, want %v", got.ModelScore, want)
	}
	if got.Outcome != scoring.OutcomeReview {
		t.Errorf("outcome = %s, want review: the blended score %v is above flag_at", got.Outcome, want)
	}
	d := purchases.decisions[0]
	if d.ModelScoreUnblended == nil || *d.ModelScoreUnblended != 0.2 || *d.ModelScore != want {
		t.Errorf("saved scores %v / %v, want blended %v and unblended 0.2", d.ModelScore, d.ModelScoreUnblended, want)
	}
}

func TestBlendNeedsEarlierScores(t *testing.T) {
	boom := errors.New("database is down")
	purchases := &fakePurchases{earlierErr: boom}
	h := NewHandler(fakeRulesets{rs: testRuleset}, &fakeBlocklist{}, purchases)
	if err := h.UseModel(&fakeModel{score: 0.2}, scoring.ModelPolicy{FlagAt: 0.6, ClearBelow: 0.01, PlayerBlendWeight: 0.5}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, testKey)
	req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(minimalBody))
	req.Header.Set("X-API-Key", testKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || len(purchases.saved) != 0 {
		t.Fatalf("status %d, saved %d; want 500 and nothing saved", rec.Code, len(purchases.saved))
	}
}

// fakeCombination has two models; Combine returns the higher of the two scores.
type fakeCombination struct {
	fakeModel
	scores   []float64
	combined []float64
}

func (c *fakeCombination) Scores([]float64) ([]float64, error) {
	return append([]float64(nil), c.scores...), nil
}

func (c *fakeCombination) Combine(scores []float64) float64 {
	c.combined = scores
	return max(scores[0], scores[1])
}

func TestBlendAppliesToTheMainModelOfACombination(t *testing.T) {
	purchases := &fakePurchases{earlierSum: 1.6, earlierCount: 2}
	model := &fakeCombination{scores: []float64{0.2, 0.3}}
	h := NewHandler(fakeRulesets{rs: testRuleset}, &fakeBlocklist{}, purchases)
	if err := h.UseModel(model, scoring.ModelPolicy{FlagAt: 0.9, ClearBelow: 0.01, PlayerBlendWeight: 0.5}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, testKey)
	req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(minimalBody))
	req.Header.Set("X-API-Key", testKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	blendedMain := 0.5*0.2 + 0.5*0.8
	if model.combined[0] != blendedMain || model.combined[1] != 0.3 {
		t.Errorf("combined scores %v, want the main model blended to %v and the other unchanged", model.combined, blendedMain)
	}
	got := decodeJSON[scoreResponse](t, rec)
	if got.ModelScore == nil || *got.ModelScore != blendedMain {
		t.Errorf("model_score = %v, want %v", got.ModelScore, blendedMain)
	}
	d := purchases.decisions[0]
	if d.ModelScoreUnblended == nil || *d.ModelScoreUnblended != 0.2 {
		t.Errorf("saved unblended score %v, want the main model's own 0.2", d.ModelScoreUnblended)
	}
}
