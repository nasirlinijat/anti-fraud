package transport

import (
	"net/http"
	"slices"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

type fakeExplainer struct {
	got []float64
}

func (f *fakeExplainer) Version() string { return "demo-model" }
func (f *fakeExplainer) Features() []string {
	return []string{"card_uses_1h", "amount", "account_age_days"}
}
func (f *fakeExplainer) Explain(x []float64, top int) ([]models.ModelReason, error) {
	f.got = x
	return []models.ModelReason{{Feature: "card_uses_1h", Value: x[0], Effect: 0.8}, {Feature: "amount", Value: x[1], Effect: -0.2}}[:min(top, 2)], nil
}

func newExplainingReviewMux(t *testing.T, explainer ModelExplainer) (*http.ServeMux, *mocks.MockReviewService) {
	t.Helper()
	service := mocks.NewMockReviewService(gomock.NewController(t))
	mux := http.NewServeMux()
	RegisterReviewRoutes(mux, NewReviewHandler(service, true).UseExplainer(explainer), platform.Access{Sessions: consoleTestSessions})
	return mux, service
}

func modelCase(version string) models.CaseItem {
	score := 0.82
	return models.CaseItem{
		QueueItem:   models.QueueItem{PurchaseID: 9, TransactionID: "explained", ModelScore: &score, ModelVersion: &version},
		ModelInputs: map[string]float64{"card_uses_1h": 6, "amount": 499.5},
	}
}

func TestReviewCaseExplainsTheModelScore(t *testing.T) {
	explainer := &fakeExplainer{}
	mux, service := newExplainingReviewMux(t, explainer)
	service.EXPECT().Case(gomock.Any(), int64(9)).Return(modelCase("demo-model"), nil)

	rec := send(mux, http.MethodGet, "/reviews/9", mockReviewerToken, "")
	wantResponse(t, rec, http.StatusOK)
	got := decodeJSON[caseResponse](t, rec)
	want := []modelReasonResponse{{Feature: "card_uses_1h", Value: 6, Effect: 0.8}, {Feature: "amount", Value: 499.5, Effect: -0.2}}
	if !slices.Equal(got.ModelReasons, want) {
		t.Fatalf("model reasons = %+v, want %+v", got.ModelReasons, want)
	}
	if !slices.Equal(explainer.got, []float64{6, 499.5, features.Missing}) {
		t.Fatalf("explained vector = %v, want the saved inputs in model order with missing values marked", explainer.got)
	}
}

func TestReviewCaseHasNoReasonsWithoutAMatchingModel(t *testing.T) {
	noScore := modelCase("demo-model")
	noScore.ModelScore = nil
	cases := []struct {
		name      string
		explainer ModelExplainer
		item      models.CaseItem
	}{
		{"another model version", &fakeExplainer{}, modelCase("older-model")},
		{"rules-only decision", &fakeExplainer{}, noScore},
		{"no model loaded", nil, modelCase("demo-model")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mux, service := newExplainingReviewMux(t, c.explainer)
			service.EXPECT().Case(gomock.Any(), int64(9)).Return(c.item, nil)
			rec := send(mux, http.MethodGet, "/reviews/9", mockReviewerToken, "")
			wantResponse(t, rec, http.StatusOK, `"model_reasons":[]`)
		})
	}
}

func TestResolvedCaseAlsoExplainsTheModelScore(t *testing.T) {
	mux, service := newExplainingReviewMux(t, &fakeExplainer{})
	item := modelCase("demo-model")
	item.Resolution = &models.CaseResolution{Decision: "reject", AnalystID: "reviewer-one"}
	service.EXPECT().ResolvedCase(gomock.Any(), int64(9)).Return(item, nil)
	rec := send(mux, http.MethodGet, "/reviews/9/resolved", mockReviewerToken, "")
	wantResponse(t, rec, http.StatusOK, `"model_reasons":[{"feature":"card_uses_1h","value":6,"effect":0.8}`)
}
