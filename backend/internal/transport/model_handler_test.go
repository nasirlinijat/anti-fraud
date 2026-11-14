package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func TestModelStatusReadsTheActiveRuleset(t *testing.T) {
	settings := scoring.ModelSettings{Mode: scoring.ModelAssist}
	model := &LoadedModel{
		Version: "test-model", Policy: scoring.ModelPolicy{ClearBelow: 0.1, FlagAt: 0.6},
		Evaluation: &models.ModelEvaluation{Dataset: "Test dataset", ROCAUC: 0.9, PRAUC: 0.8},
	}
	mux := http.NewServeMux()
	RegisterModelRoutes(mux, func(context.Context) (scoring.Ruleset, error) {
		return scoring.Ruleset{Model: settings}, nil
	}, model, platform.Access{Sessions: resetSessions{platform.RoleReviewer}})

	status := requestModelStatus(t, mux)
	if !status.Loaded || status.Mode != scoring.ModelAssist || status.Version == nil || *status.Version != model.Version ||
		status.Evaluation == nil || *status.Evaluation != *model.Evaluation ||
		status.ClearBelow == nil || *status.ClearBelow != 0.1 || status.FlagAt == nil || *status.FlagAt != 0.6 {
		t.Fatalf("unexpected model status: %+v", status)
	}

	clearBelow, flagAt := 0.2, 0.8
	settings = scoring.ModelSettings{Mode: scoring.ModelShadow, ClearBelow: &clearBelow, FlagAt: &flagAt}
	status = requestModelStatus(t, mux)
	if status.Mode != scoring.ModelShadow || status.ClearBelow == nil || *status.ClearBelow != 0.2 ||
		status.FlagAt == nil || *status.FlagAt != 0.8 {
		t.Fatalf("unexpected model status after publishing: %+v", status)
	}
}

func TestModelStatusWithoutAModel(t *testing.T) {
	mux := http.NewServeMux()
	RegisterModelRoutes(mux, func(context.Context) (scoring.Ruleset, error) {
		return scoring.Ruleset{Model: scoring.ModelSettings{Mode: scoring.ModelAssist}}, nil
	}, nil, platform.Access{Sessions: resetSessions{platform.RoleReviewer}})
	status := requestModelStatus(t, mux)
	if status.Loaded || status.Mode != scoring.ModelAssist || status.Version != nil ||
		status.ClearBelow != nil || status.FlagAt != nil || status.Evaluation != nil {
		t.Fatalf("unexpected model status: %+v", status)
	}
}

func TestModelStatusRequiresReviewAccess(t *testing.T) {
	mux := http.NewServeMux()
	RegisterModelRoutes(mux, nil, nil, platform.Access{Sessions: consoleTestSessions})
	req := httptest.NewRequest(http.MethodGet, "/model", nil)
	setTestCredential(req, serviceTestKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	wantResponse(t, rec, http.StatusUnauthorized)
}

func requestModelStatus(t *testing.T, mux *http.ServeMux) modelStatus {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/model", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	wantResponse(t, rec, http.StatusOK)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	var status modelStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}
