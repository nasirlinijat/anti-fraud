package transport

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

func newNetworkPurchasesMux(t *testing.T) (*http.ServeMux, *mocks.MockReviewService) {
	t.Helper()
	service := mocks.NewMockReviewService(gomock.NewController(t))
	mux := http.NewServeMux()
	RegisterReviewRoutes(mux, NewReviewHandler(service, true), platform.Access{Sessions: consoleTestSessions})
	return mux, service
}

func TestNetworkPurchasesSerializesThePurchases(t *testing.T) {
	mux, service := newNetworkPurchasesMux(t)
	decision, holder := "reject", "alice"
	service.EXPECT().NetworkPurchases(gomock.Any(), int64(9007199254740993), models.BlocklistDevice, "player 2").Return(models.LinkedPurchases{
		Anchor: models.BlocklistDevice, Value: "player 2", WindowDays: 30, Total: 51,
		Purchases: []models.LinkedPurchase{
			{PurchaseID: 12, TransactionID: "tx-12", EventTime: time.Date(2026, 11, 1, 12, 0, 0, 0, time.FixedZone("CET", 3600)),
				AmountMinor: 1999, Outcome: scoring.OutcomeReview, ClaimedBy: &holder, AwaitingReview: true},
			{PurchaseID: 11, TransactionID: "tx-11", EventTime: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC),
				AmountMinor: 500, Outcome: scoring.OutcomeReview, ReviewDecision: &decision},
		},
	}, nil)

	w := send(mux, http.MethodGet, "/reviews/9007199254740993/network/purchases?anchor=device&value=player%202", queueTestKey, "")

	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
	}
	want := `{"anchor":"device","value":"player 2","window_days":30,"total":51,"purchases":[` +
		`{"purchase_id":"12","transaction_id":"tx-12","event_time":"2026-11-01T11:00:00Z","amount_minor":"1999","outcome":"review","review_decision":null,"claimed_by":"alice","awaiting_review":true},` +
		`{"purchase_id":"11","transaction_id":"tx-11","event_time":"2026-10-30T09:00:00Z","amount_minor":"500","outcome":"review","review_decision":"reject","claimed_by":null,"awaiting_review":false}]}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}
}

func TestNetworkPurchasesWithoutPurchasesIsAnEmptyList(t *testing.T) {
	mux, service := newNetworkPurchasesMux(t)
	service.EXPECT().NetworkPurchases(gomock.Any(), int64(5), models.BlocklistPlayer, "card-2").
		Return(models.LinkedPurchases{Anchor: models.BlocklistPlayer, Value: "card-2", WindowDays: 30}, nil)

	w := send(mux, http.MethodGet, "/reviews/5/network/purchases?anchor=player&value=card-2", queueTestKey, "")

	wantResponse(t, w, http.StatusOK, `"total":0`, `"purchases":[]`)
}

func TestNetworkPurchasesRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		key    string
		status int
		body   string
	}{
		{"missing key", "/reviews/1/network/purchases?anchor=card&value=p", "", http.StatusUnauthorized, ""},
		{"service key", "/reviews/1/network/purchases?anchor=card&value=p", serviceTestKey, http.StatusUnauthorized, ""},
		{"invalid purchase ID", "/reviews/01/network/purchases?anchor=card&value=p", queueTestKey, http.StatusBadRequest, "invalid purchase ID"},
		{"invalid query", "/reviews/1/network/purchases?anchor=card&value=%zz", queueTestKey, http.StatusBadRequest, "invalid query parameters"},
		{"missing anchor", "/reviews/1/network/purchases?value=p", queueTestKey, http.StatusBadRequest, "anchor must be given once"},
		{"repeated anchor", "/reviews/1/network/purchases?anchor=card&anchor=card&value=p", queueTestKey, http.StatusBadRequest, "anchor must be given once"},
		{"missing value", "/reviews/1/network/purchases?anchor=card", queueTestKey, http.StatusBadRequest, "value must be given once"},
		{"repeated value", "/reviews/1/network/purchases?anchor=card&value=p&value=q", queueTestKey, http.StatusBadRequest, "value must be given once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, service := newNetworkPurchasesMux(t)
			service.EXPECT().NetworkPurchases(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

			w := send(mux, http.MethodGet, tc.target, tc.key, "")

			wantResponse(t, w, tc.status, tc.body)
		})
	}
}

func TestNetworkPurchasesMapsServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"invalid anchor", review.ErrInvalidNetworkAnchor, http.StatusBadRequest, `{"error":"anchor must be device, card, or player"}`},
		{"invalid value", review.ErrInvalidLinkedValue, http.StatusBadRequest, `{"error":"value must be 1 to 256 bytes without control characters"}`},
		{"not under review", models.ErrReviewNotFound, http.StatusNotFound, `{"error":"review case not found"}`},
		{"database down", errServiceDown, http.StatusInternalServerError, `{"error":"internal error"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, service := newNetworkPurchasesMux(t)
			service.EXPECT().NetworkPurchases(gomock.Any(), int64(5), models.BlocklistType("ip"), "").Return(models.LinkedPurchases{}, tc.err)

			w := send(mux, http.MethodGet, "/reviews/5/network/purchases?anchor=ip&value=", queueTestKey, "")

			if w.Code != tc.status || strings.TrimSpace(w.Body.String()) != tc.body {
				t.Fatalf("status = %d, body = %s; want %d %s", w.Code, w.Body, tc.status, tc.body)
			}
		})
	}
}
