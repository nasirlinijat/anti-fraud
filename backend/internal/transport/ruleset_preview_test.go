package transport

import (
	"net/http"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

const previewBody = `{"review_threshold":50,"block_threshold":80,"note":"n","rules":{"high_amount":{"enabled":true,"weight":60,"limit":5000}}}`

func newPreviewMux(t *testing.T) (*http.ServeMux, *mocks.MockRulesetService) {
	t.Helper()
	service := mocks.NewMockRulesetService(gomock.NewController(t))
	mux := http.NewServeMux()
	RegisterRulesetRoutes(mux, NewRulesetHandler(service), platform.Access{Sessions: consoleTestSessions})
	return mux, service
}

func TestRulesetPreviewReturnsTheImpact(t *testing.T) {
	mux, service := newPreviewMux(t)
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	draft := gomock.Cond(func(d ruleset.Draft) bool {
		rule := d.Rules[scoring.RuleHighAmount]
		return d.ReviewThreshold == 50 && rule != nil && *rule.Weight == 60
	})
	service.EXPECT().Preview(gomock.Any(), draft).Return(models.RulesetPreview{
		Decisions: 1000, Before: models.PreviewCounts{Allow: 900, Review: 80, Block: 20}, After: models.PreviewCounts{Allow: 890, Review: 70, Block: 40},
		Changed: 30, Uncertain: 2, UncertainRules: []string{"new_account"}, MissingModelScores: 1,
		Changes: []models.PreviewChange{{PurchaseID: 55, TransactionID: "txn-55", DecidedAt: at, Before: "review", After: "block", RuleScoreBefore: 60, RuleScoreAfter: 90}},
	}, nil)

	wantResponse(t, send(mux, http.MethodPost, "/rulesets/preview", mockAdminToken, previewBody), http.StatusOK,
		`"decisions":1000`, `"before":{"allow":900,"review":80,"block":20}`, `"after":{"allow":890,"review":70,"block":40}`,
		`"changed":30`, `"uncertain":2`, `"uncertain_rules":["new_account"]`, `"missing_model_scores":1`,
		`"changes":[{"purchase_id":"55","transaction_id":"txn-55","decided_at":"2026-11-14T10:00:00Z","before":"review","after":"block","rule_score_before":60,"rule_score_after":90,"uncertain":false}]`)
}

func TestRulesetPreviewValidatesAndIsForAdmins(t *testing.T) {
	mux, service := newPreviewMux(t)
	service.EXPECT().Preview(gomock.Any(), gomock.Any()).Return(models.RulesetPreview{}, &ruleset.ValidationError{Fields: map[string]string{"block_threshold": "must be positive"}})
	wantResponse(t, send(mux, http.MethodPost, "/rulesets/preview", mockAdminToken, previewBody), http.StatusBadRequest, `"block_threshold":"must be positive"`)
	wantResponse(t, send(mux, http.MethodPost, "/rulesets/preview", mockAdminToken, `{"unknown":1}`), http.StatusBadRequest)
	wantResponse(t, send(mux, http.MethodPost, "/rulesets/preview", mockReviewerToken, previewBody), http.StatusForbidden)
	wantResponse(t, send(mux, http.MethodPost, "/rulesets/preview", "", previewBody), http.StatusUnauthorized)
}
