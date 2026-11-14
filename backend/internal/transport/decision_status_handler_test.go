package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/decisionstatus"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

func statusMux(t *testing.T, limits platform.ScoreLimits) (*http.ServeMux, *mocks.MockDecisionStatusService) {
	t.Helper()
	s := mocks.NewMockDecisionStatusService(gomock.NewController(t))
	mux := http.NewServeMux()
	RegisterDecisionStatusRoutes(mux, NewDecisionStatusHandler(s).UseLimits(limits), mockServiceKey)
	return mux, s
}
func TestStatusReturnsOnlyMinimalFields(t *testing.T) {
	mux, s := statusMux(t, platform.ScoreLimits{})
	s.EXPECT().Lookup(gomock.Any(), "a+b&c").Return(models.DecisionStatus{TransactionID: "a+b&c", Outcome: "review", ReviewStatus: "pending"}, nil)
	rec := send(mux, "GET", "/decision-status?transaction_id=+a%2Bb%26c+", mockServiceKey, "")
	wantResponse(t, rec, 200)
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"transaction_id": "a+b&c", "outcome": "review", "review_status": "pending", "resolved_at": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response %+v, want %+v", got, want)
	}

	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
}
func TestStatusRejectsInvalidQueriesBeforeLookup(t *testing.T) {
	mux, _ := statusMux(t, platform.ScoreLimits{})
	for _, q := range []string{"", "transaction_id=+", "transaction_id=x&transaction_id=x", "transaction_id=%zz", "transaction_id=%00", "transaction_id=%FF"} {
		wantResponse(t, send(mux, "GET", "/decision-status?"+q, mockServiceKey, ""), 400)
	}
	for _, key := range []string{"", "wrong", mockReviewerToken, mockAdminToken} {
		wantResponse(t, send(mux, "GET", "/decision-status?transaction_id=x", key, ""), 401)
	}
	session := httptest.NewRequest("GET", "/decision-status?transaction_id=x", nil)
	session.Header.Set("Authorization", "Bearer "+mockAdminToken)
	sessionResponse := httptest.NewRecorder()
	mux.ServeHTTP(sessionResponse, session)
	wantResponse(t, sessionResponse, 401)
	req := httptest.NewRequest("GET", "/decision-status?transaction_id=x", nil)
	req.Header.Add("X-API-Key", mockServiceKey)
	req.Header.Add("X-API-Key", mockServiceKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	wantResponse(t, rec, 401)
}
func TestStatusAcceptsLongExistingIdentifiers(t *testing.T) {
	mux, s := statusMux(t, platform.ScoreLimits{})
	id := strings.Repeat("x", 300)
	s.EXPECT().Lookup(gomock.Any(), id).Return(models.DecisionStatus{}, models.ErrPurchaseNotFound)
	wantResponse(t, send(mux, "GET", "/decision-status?transaction_id="+id, mockServiceKey, ""), 404)
}
func TestStatusLimitsAreIndependentOfScoring(t *testing.T) {
	mux, s := statusMux(t, platform.ScoreLimits{RatePerSecond: 0.001, Burst: 1})
	score := mocks.NewMockScoreService(gomock.NewController(t))
	RegisterScoreRoutes(mux, NewScoreHandler(score).UseLimits(platform.ScoreLimits{RatePerSecond: 0.001, Burst: 1}), mockServiceKey)
	s.EXPECT().Lookup(gomock.Any(), "x").Return(models.DecisionStatus{}, models.ErrPurchaseNotFound)
	wantResponse(t, send(mux, "GET", "/decision-status?transaction_id=x", "wrong", ""), 401)
	wantResponse(t, send(mux, "GET", "/decision-status?transaction_id=x", mockServiceKey, ""), 404)
	rec := send(mux, "GET", "/decision-status?transaction_id=x", mockServiceKey, "")
	wantResponse(t, rec, 429, "status requests")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing retry delay")
	}
	score.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.Decision{}, nil)
	wantResponse(t, send(mux, "POST", "/score", mockServiceKey, minimalBody), 200)
}
func TestStatusDeadlineAndInternalErrors(t *testing.T) {
	mux, s := statusMux(t, platform.ScoreLimits{Timeout: time.Millisecond})
	s.EXPECT().Lookup(gomock.Any(), "x").DoAndReturn(func(ctx context.Context, _ string) (models.DecisionStatus, error) {
		<-ctx.Done()
		return models.DecisionStatus{}, ctx.Err()
	})
	rec := send(mux, "GET", "/decision-status?transaction_id=x", mockServiceKey, "")
	wantResponse(t, rec, 503)
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatal("retry")
	}
	s.EXPECT().Lookup(gomock.Any(), "x").Return(models.DecisionStatus{}, fmt.Errorf("private failure"))
	rec = send(mux, "GET", "/decision-status?transaction_id=x", mockServiceKey, "")
	wantResponse(t, rec, 500)
	if strings.Contains(rec.Body.String(), "private") {
		t.Fatal("leaked error")
	}
}
func TestStatusUnavailableDatabaseReturns503(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	mux := http.NewServeMux()
	RegisterDecisionStatusRoutes(mux, NewDecisionStatusHandler(decisionstatus.NewService(storage.NewDecisionStatusStore(pool))), mockServiceKey)
	wantResponse(t, send(mux, "GET", "/decision-status?transaction_id=x", mockServiceKey, ""), 503)
}
