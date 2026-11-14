package transport

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"go.uber.org/mock/gomock"
)

func TestPurchaseSearchReturnsMatchesWithTheirReviewState(t *testing.T) {
	mux, service := newMockReviewMux(t)
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	service.EXPECT().SearchPurchases(gomock.Any(), "player-381").Return([]models.PurchaseMatch{
		{PurchaseID: 55, TransactionID: "txn-55", PlayerID: "player-381", AmountMinor: 72609, EventTime: at, Outcome: "review", Review: "open", MatchedBy: "player"},
		{PurchaseID: 54, TransactionID: "txn-54", PlayerID: "player-381", AmountMinor: 100, EventTime: at, Outcome: "allow", MatchedBy: "player"},
	}, nil)

	rec := send(mux, http.MethodGet, "/purchases/search?q=+player-381+", mockReviewerToken, "")
	wantResponse(t, rec, http.StatusOK)
	got := decodeJSON[purchaseSearchResponse](t, rec)
	if len(got.Items) != 2 || got.Items[0].PurchaseID != "55" || got.Items[0].AmountMinor != "72609" || got.Items[0].Review == nil ||
		*got.Items[0].Review != "open" || got.Items[1].Review != nil || got.Items[1].MatchedBy != "player" {
		t.Fatalf("search = %+v", got.Items)
	}
}

func TestPurchaseSearchValidatesTheQueryAndNeedsASession(t *testing.T) {
	mux, service := newMockReviewMux(t)
	for _, query := range []string{"", "+++", strings.Repeat("x", 201)} {
		wantResponse(t, send(mux, http.MethodGet, "/purchases/search?q="+query, mockReviewerToken, ""), http.StatusBadRequest, `"q":"must be 1 to 200 characters"`)
	}
	service.EXPECT().SearchPurchases(gomock.Any(), "nothing").Return(nil, nil)
	wantResponse(t, send(mux, http.MethodGet, "/purchases/search?q=nothing", mockAdminToken, ""), http.StatusOK, `"items":[]`)
	wantResponse(t, send(mux, http.MethodGet, "/purchases/search?q=x", "", ""), http.StatusUnauthorized)
}

func TestPurchaseDetailsForAnyOutcome(t *testing.T) {
	mux, service := newMockReviewMux(t)
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	service.EXPECT().PurchaseCase(gomock.Any(), int64(54)).Return(models.PurchaseCase{
		CaseItem: models.CaseItem{QueueItem: models.QueueItem{PurchaseID: 54, TransactionID: "txn-54"}},
		Outcome:  "allow",
	}, nil)
	service.EXPECT().PurchaseCase(gomock.Any(), int64(55)).Return(models.PurchaseCase{
		CaseItem: models.CaseItem{
			QueueItem:  models.QueueItem{PurchaseID: 55, TransactionID: "txn-55"},
			Resolution: &models.CaseResolution{Decision: "reject", AnalystID: "reviewer-two", ResolvedAt: at},
		},
		Outcome: "review",
	}, nil)
	service.EXPECT().PurchaseCase(gomock.Any(), int64(56)).Return(models.PurchaseCase{}, models.ErrPurchaseNotFound)

	wantResponse(t, send(mux, http.MethodGet, "/purchases/54", mockReviewerToken, ""), http.StatusOK,
		`"transaction_id":"txn-54"`, `"outcome":"allow"`, `"resolution":null`, `"model_reasons":[]`)
	wantResponse(t, send(mux, http.MethodGet, "/purchases/55", mockReviewerToken, ""), http.StatusOK,
		`"outcome":"review"`, `"resolution":{"decision":"reject","analyst_id":"reviewer-two","resolved_at":"2026-11-14T10:00:00Z"}`)
	wantResponse(t, send(mux, http.MethodGet, "/purchases/56", mockReviewerToken, ""), http.StatusNotFound, `"purchase not found"`)
	wantResponse(t, send(mux, http.MethodGet, "/purchases/abc", mockReviewerToken, ""), http.StatusBadRequest)
}
