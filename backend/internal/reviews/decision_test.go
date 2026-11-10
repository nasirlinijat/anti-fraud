package reviews

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/stats"
)

type rejectUnexpectedReviewAccess struct{}

func (rejectUnexpectedReviewAccess) queue(context.Context, int, *queueCursor) ([]QueueItem, error) {
	panic("unexpected queue read")
}

func (rejectUnexpectedReviewAccess) Case(context.Context, int64) (CaseItem, error) {
	panic("unexpected case read")
}

func (rejectUnexpectedReviewAccess) resolve(context.Context, int64, string, string) (resolution, error) {
	panic("unexpected resolution write")
}

func reviewRequest(mux *http.ServeMux, method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set(platform.APIKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestReviewRoutesRejectUnconfiguredAccess(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, rejectUnexpectedReviewAccess{}, platform.AnalystKeys{})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/reviews"},
		{http.MethodGet, "/reviews/1"},
		{http.MethodPost, "/reviews/1/decision"},
	} {
		rec := reviewRequest(mux, route.method, route.path, queueTestKey, `{"decision":"approve"}`)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s response = %d %s", route.method, route.path, rec.Code, rec.Body)
		}
	}
}

func TestReviewDecisionRejectsInvalidRequests(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, rejectUnexpectedReviewAccess{}, testAnalystKeys(t))
	const target = "/reviews/1/decision"
	const body = `{"decision":"approve"}`
	for _, tc := range []struct {
		name, path, key, body string
		status                int
	}{
		{"missing key", target, "", body, 401},
		{"shared key", target, serviceTestKey, body, 401},
		{"invalid key", target, "wrong", body, 401},
		{"invalid ID", "/reviews/-1/decision", queueTestKey, body, 400},
		{"ID overflow", "/reviews/9223372036854775808/decision", queueTestKey, body, 400},
		{"invalid decision", target, queueTestKey, `{"decision":"allow"}`, 400},
		{"spoofed analyst", target, queueTestKey, `{"decision":"approve","analyst_id":"someone-else"}`, 400},
		{"trailing object", target, queueTestKey, body + "{}", 400},
		{"oversized body", target, queueTestKey, body + strings.Repeat(" ", maxDecisionBytes), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := reviewRequest(mux, http.MethodPost, tc.path, tc.key, tc.body)
			if rec.Code != tc.status || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.status, rec.Body)
			}
		})
	}
}

func TestReviewDecisionsAgainstPostgres(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	at := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	score := 0.7
	first := insertDecision(t, tx, "resolution-first", "review", at, &score, `[]`)
	second := insertDecision(t, tx, "resolution-second", "review", at, nil, `[]`)
	allowed := insertDecision(t, tx, "resolution-allowed", "allow", at, nil, `[]`)
	store := NewStore(tx)
	mux := http.NewServeMux()
	RegisterRoutes(mux, store, testAnalystKeys(t))
	summary := func() stats.Summary {
		t.Helper()
		result, err := stats.NewStore(tx).Summary(ctx, at, 10)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := summary()
	caseURL := "/reviews/" + strconv.FormatInt(first, 10)
	if rec := reviewRequest(mux, http.MethodGet, caseURL, secondAnalystTestKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("pending case = %d %s", rec.Code, rec.Body)
	}
	submit := func(id int64, key, decision string) *httptest.ResponseRecorder {
		return reviewRequest(mux, http.MethodPost, "/reviews/"+strconv.FormatInt(id, 10)+"/decision", key,
			`{"decision":"`+decision+`"}`)
	}
	for _, tc := range []struct {
		id                     int64
		key, analyst, decision string
	}{
		{first, queueTestKey, "first", "approve"},
		{second, secondAnalystTestKey, "second", "reject"},
	} {
		rec := submit(tc.id, tc.key, tc.decision)
		var saved decisionResponse
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &saved) != nil ||
			saved.PurchaseID != strconv.FormatInt(tc.id, 10) || saved.AnalystID != tc.analyst ||
			saved.Decision != tc.decision || saved.ResolvedAt.IsZero() {
			t.Fatalf("save response = %d %s", rec.Code, rec.Body)
		}
		var analystID, decision string
		var resolvedAt time.Time
		err := tx.QueryRow(ctx, "SELECT analyst_id, decision, resolved_at FROM review_resolutions WHERE purchase_id = $1", tc.id).
			Scan(&analystID, &decision, &resolvedAt)
		if err != nil || analystID != tc.analyst || decision != tc.decision || !resolvedAt.Equal(saved.ResolvedAt) {
			t.Fatalf("stored resolution = %q %q %v, err = %v", analystID, decision, resolvedAt, err)
		}
	}
	if rec := submit(first, secondAnalystTestKey, "reject"); rec.Code != http.StatusConflict {
		t.Fatalf("second decision = %d %s", rec.Code, rec.Body)
	}
	if rec := reviewRequest(mux, http.MethodGet, caseURL, secondAnalystTestKey, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("resolved case = %d %s", rec.Code, rec.Body)
	}
	if rec := submit(allowed, queueTestKey, "reject"); rec.Code != http.StatusConflict {
		t.Fatalf("non-review decision = %d %s", rec.Code, rec.Body)
	}
	if rec := submit(9223372036854775807, queueTestKey, "reject"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing purchase = %d %s", rec.Code, rec.Body)
	}
	page := readQueuePage(t, reviewRequest(mux, http.MethodGet, "/reviews", queueTestKey, ""))
	for _, item := range page.Items {
		if item.PurchaseID == strconv.FormatInt(first, 10) || item.PurchaseID == strconv.FormatInt(second, 10) {
			t.Fatalf("resolved purchase %s remained in queue", item.PurchaseID)
		}
	}
	after := summary()
	if after.ReviewQueue != before.ReviewQueue-2 || after.Outcomes != before.Outcomes || after.Model != before.Model {
		t.Fatalf("before = %+v, after = %+v", before, after)
	}
	var modelVersion, originalOutcome, originalResolution string
	var modelScore float64
	err := tx.QueryRow(ctx, `
		SELECT d.model_version, d.outcome, d.model_score, r.decision
		FROM decisions d JOIN review_resolutions r USING (purchase_id)
		WHERE d.purchase_id = $1`, first).Scan(&modelVersion, &originalOutcome, &modelScore, &originalResolution)
	if err != nil || modelVersion != testModelVersion || originalOutcome != "review" ||
		modelScore != score || originalResolution != "approve" {
		t.Fatalf("saved score or resolution was changed: %q %q %v %q, err = %v",
			modelVersion, originalOutcome, modelScore, originalResolution, err)
	}
}
