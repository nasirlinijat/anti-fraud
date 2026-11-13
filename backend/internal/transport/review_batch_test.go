package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

const batchTestToken = "alice-session-token"

// batchTestSessions knows one reviewer session, batchTestToken for alice.
type batchTestSessions struct{}

func (batchTestSessions) Identify(_ context.Context, token string) (platform.Identity, error) {
	if token != batchTestToken {
		return platform.Identity{}, platform.ErrNoSession
	}
	return platform.Identity{Username: "alice", Role: platform.RoleReviewer}, nil
}

func newBatchMux(t *testing.T) (*http.ServeMux, *mocks.MockReviewService) {
	t.Helper()
	service := mocks.NewMockReviewService(gomock.NewController(t))
	mux := http.NewServeMux()
	access := platform.Access{Analysts: testAnalystKeys(t), Sessions: batchTestSessions{}}
	RegisterReviewRoutes(mux, NewReviewHandler(service, true), access)
	return mux, service
}

func sendAsSession(mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+batchTestToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func wantBody(t *testing.T, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if got := strings.TrimSpace(rec.Body.String()); rec.Code != status || got != body {
		t.Fatalf("status = %d, body =\n%s\nwant %d\n%s", rec.Code, got, status, body)
	}
}

func TestBatchClaimReportsEachPurchase(t *testing.T) {
	mux, service := newBatchMux(t)
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.FixedZone("CET", 3600))
	service.EXPECT().ClaimMany(gomock.Any(), []int64{12, 15, 3, 4, 5, 6, 7}, "alice").Return([]review.ClaimResult{
		{PurchaseID: 12, Status: review.BatchClaimed, Claim: models.Claim{Status: models.ClaimSaved, ClaimedBy: "alice", ClaimedAt: at}},
		{PurchaseID: 15, Status: review.BatchAlreadyYours, Claim: models.Claim{Status: models.ClaimAlreadyYours, ClaimedBy: "alice", ClaimedAt: at}},
		{PurchaseID: 3, Status: review.BatchHeldByOther, Claim: models.Claim{Status: models.ClaimHeldByOther, ClaimedBy: "bob", ClaimedAt: at}},
		{PurchaseID: 4, Status: review.BatchAlreadyDecided},
		{PurchaseID: 5, Status: review.BatchNotFound},
		{PurchaseID: 6, Status: review.BatchNotReview},
		{PurchaseID: 7, Status: review.BatchError, Err: errServiceDown},
	}, nil)

	rec := sendAsSession(mux, http.MethodPost, "/reviews/claims", `{"purchase_ids":["12","15","3","4","5","6","7"]}`)

	wantBody(t, rec, http.StatusOK, `{"results":[`+
		`{"purchase_id":"12","status":"claimed","claimed_by":"alice","claimed_at":"2026-11-12T09:00:00Z"},`+
		`{"purchase_id":"15","status":"already_yours","claimed_by":"alice","claimed_at":"2026-11-12T09:00:00Z"},`+
		`{"purchase_id":"3","status":"held_by_other","claimed_by":"bob","claimed_at":"2026-11-12T09:00:00Z"},`+
		`{"purchase_id":"4","status":"already_decided","claimed_by":null,"claimed_at":null},`+
		`{"purchase_id":"5","status":"not_found","claimed_by":null,"claimed_at":null},`+
		`{"purchase_id":"6","status":"not_review","claimed_by":null,"claimed_at":null},`+
		`{"purchase_id":"7","status":"error","claimed_by":null,"claimed_at":null}]}`)
}

func TestBatchClaimNeedsASession(t *testing.T) {
	mux, service := newBatchMux(t)
	service.EXPECT().ClaimMany(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	rec := send(mux, http.MethodPost, "/reviews/claims", queueTestKey, `{"purchase_ids":["12"]}`)

	wantBody(t, rec, http.StatusForbidden, `{"error":"claiming a purchase needs a signed-in account"}`)
}

func TestBatchDecisionWithASessionDecidesClaimedPurchases(t *testing.T) {
	mux, service := newBatchMux(t)
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	service.EXPECT().DecideManyClaimed(gomock.Any(), review.BatchDecision{
		PurchaseIDs: []int64{12, 15, 3, 4, 5, 6}, Decision: "reject", AnalystID: "alice", Blocks: models.ReviewBlocks{Card: true, Player: true},
	}).Return([]review.DecisionResult{
		{PurchaseID: 12, Status: review.BatchSaved, Resolution: models.Resolution{
			Status: models.ResolutionSaved, ResolvedAt: at, Blocked: models.ReviewBlocks{Card: true, Player: true},
		}},
		{PurchaseID: 15, Status: review.BatchAlreadyDecided},
		{PurchaseID: 3, Status: review.BatchNotClaimed},
		{PurchaseID: 4, Status: review.BatchNotFound},
		{PurchaseID: 5, Status: review.BatchNotReview},
		{PurchaseID: 6, Status: review.BatchError, Err: errServiceDown},
	}, nil)

	rec := sendAsSession(mux, http.MethodPost, "/reviews/decisions",
		`{"decision":"reject","purchase_ids":["12","15","3","4","5","6"],"block_card":true,"block_player":true}`)

	unsaved := `"resolved_at":null,"device_blocked":false,"card_blocked":false,"player_blocked":false}`
	wantBody(t, rec, http.StatusOK, `{"decision":"reject","analyst_id":"alice","results":[`+
		`{"purchase_id":"12","status":"saved","resolved_at":"2026-11-12T10:00:00Z","device_blocked":false,"card_blocked":true,"player_blocked":true},`+
		`{"purchase_id":"15","status":"already_decided",`+unsaved+`,`+
		`{"purchase_id":"3","status":"not_claimed",`+unsaved+`,`+
		`{"purchase_id":"4","status":"not_found",`+unsaved+`,`+
		`{"purchase_id":"5","status":"not_review",`+unsaved+`,`+
		`{"purchase_id":"6","status":"error",`+unsaved+`]}`)
}

func TestBatchDecisionWithAKeyNeedsNoClaim(t *testing.T) {
	mux, service := newBatchMux(t)
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	service.EXPECT().DecideMany(gomock.Any(), review.BatchDecision{PurchaseIDs: []int64{12}, Decision: "approve", AnalystID: "first"}).
		Return([]review.DecisionResult{{PurchaseID: 12, Status: review.BatchSaved, Resolution: models.Resolution{Status: models.ResolutionSaved, ResolvedAt: at}}}, nil)

	rec := send(mux, http.MethodPost, "/reviews/decisions", queueTestKey, `{"decision":"approve","purchase_ids":["12"]}`)

	wantBody(t, rec, http.StatusOK, `{"decision":"approve","analyst_id":"first","results":[`+
		`{"purchase_id":"12","status":"saved","resolved_at":"2026-11-12T10:00:00Z","device_blocked":false,"card_blocked":false,"player_blocked":false}]}`)
}

func TestBatchRequestsRejectInvalidBodies(t *testing.T) {
	notCanonical := `{"error":"purchase_ids must hold positive decimal purchase IDs"}`
	for _, tc := range []struct {
		name, body, want string
	}{
		{"not JSON", `{`, `{"error":"request body must be a single %s object"}`},
		{"unknown field", `{"purchase_ids":["1"],"note":"x"}`, `{"error":"request body must be a single %s object"}`},
		{"two objects", `{"purchase_ids":["1"]} {}`, `{"error":"request body must be a single %s object"}`},
		{"number IDs", `{"purchase_ids":[1]}`, `{"error":"request body must be a single %s object"}`},
		{"leading zero", `{"purchase_ids":["01"]}`, notCanonical},
		{"zero", `{"purchase_ids":["0"]}`, notCanonical},
		{"negative", `{"purchase_ids":["-1"]}`, notCanonical},
		{"plus sign", `{"purchase_ids":["+1"]}`, notCanonical},
		{"null ID", `{"purchase_ids":[null]}`, notCanonical},
	} {
		for _, endpoint := range []struct{ path, what string }{
			{"/reviews/claims", "batch claim"},
			{"/reviews/decisions", "batch decision"},
		} {
			t.Run(endpoint.what+" "+tc.name, func(t *testing.T) {
				mux, _ := newBatchMux(t)

				rec := sendAsSession(mux, http.MethodPost, endpoint.path, tc.body)

				wantBody(t, rec, http.StatusBadRequest, strings.Replace(tc.want, "%s", endpoint.what, 1))
			})
		}
	}
}

func TestBatchRequestsMapServiceRefusals(t *testing.T) {
	for _, err := range []error{review.ErrInvalidBatchSize, review.ErrDuplicatePurchaseID, review.ErrInvalidDecision, review.ErrBlockNeedsReject} {
		t.Run(err.Error(), func(t *testing.T) {
			mux, service := newBatchMux(t)
			service.EXPECT().ClaimMany(gomock.Any(), gomock.Any(), "alice").Return(nil, err)
			service.EXPECT().DecideManyClaimed(gomock.Any(), gomock.Any()).Return(nil, err)
			want := `{"error":"` + err.Error() + `"}`

			wantBody(t, sendAsSession(mux, http.MethodPost, "/reviews/claims", `{"purchase_ids":[]}`), http.StatusBadRequest, want)
			wantBody(t, sendAsSession(mux, http.MethodPost, "/reviews/decisions", `{"decision":"approve","purchase_ids":["1","1"]}`), http.StatusBadRequest, want)
		})
	}
}

func TestBatchRequestsRejectTooLargeBodies(t *testing.T) {
	ids := strings.Repeat(`"9223372036854775807",`, 200)
	body := `{"purchase_ids":[` + strings.TrimSuffix(ids, ",") + `]}`
	for _, endpoint := range []struct{ path, what string }{
		{"/reviews/claims", "batch claim"},
		{"/reviews/decisions", "batch decision"},
	} {
		t.Run(endpoint.what, func(t *testing.T) {
			mux, _ := newBatchMux(t)

			rec := sendAsSession(mux, http.MethodPost, endpoint.path, body)

			wantBody(t, rec, http.StatusRequestEntityTooLarge, `{"error":"`+endpoint.what+` body is too large"}`)
		})
	}
}
