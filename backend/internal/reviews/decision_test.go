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

	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/rulesets"
	"github.com/nasirlinijat/anti-fraud/backend/internal/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/stats"
)

type rejectUnexpectedReviewAccess struct{}

func (rejectUnexpectedReviewAccess) queue(context.Context, int, *queueCursor) ([]QueueItem, error) {
	panic("unexpected queue read")
}

func (rejectUnexpectedReviewAccess) Case(context.Context, int64) (CaseItem, error) {
	panic("unexpected case read")
}

func (rejectUnexpectedReviewAccess) resolve(context.Context, int64, string, string, bool) (resolution, error) {
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

func TestRejectCanBlockTheDevice(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	at := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	withDevice := func(txnID, device string) int64 {
		t.Helper()
		id := insertDecision(t, tx, txnID, "review", at, nil, `[]`)
		if _, err := tx.Exec(ctx, `UPDATE purchases SET device_id = $2 WHERE id = $1`, id, device); err != nil {
			t.Fatalf("set device ID: %v", err)
		}
		return id
	}
	blocked := func(device string) bool {
		t.Helper()
		_, found, err := blocklist.NewStore(tx).Match(ctx, blocklist.Subject{CardToken: "none", PlayerID: "none", DeviceID: &device})
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewStore(tx), testAnalystKeys(t))
	decide := func(id int64, body string) *httptest.ResponseRecorder {
		return reviewRequest(mux, http.MethodPost, "/reviews/"+strconv.FormatInt(id, 10)+"/decision", queueTestKey, body)
	}
	const reject = `{"decision":"reject","block_device":true}`

	// A rejection without the option leaves the device alone.
	plain := withDevice("block-plain", "device-plain")
	if rec := decide(plain, `{"decision":"reject"}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"device_blocked":false`) || blocked("device-plain") {
		t.Fatalf("plain rejection = %d %s, blocked = %v", rec.Code, rec.Body, blocked("device-plain"))
	}

	// With the option, the rejection and the entry are saved together, and the next purchase from the device is blocked.
	first := withDevice("block-first", "device-bad")
	if rec := decide(first, reject); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"device_blocked":true`) || !blocked("device-bad") {
		t.Fatalf("rejection with the option = %d %s, blocked = %v", rec.Code, rec.Body, blocked("device-bad"))
	}

	// A device that is already blocked adds no second entry, and the rejection still succeeds.
	again := withDevice("block-again", "device-bad")
	if rec := decide(again, reject); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"device_blocked":false`) {
		t.Fatalf("rejection of an already blocked device = %d %s", rec.Code, rec.Body)
	}
	var entries int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM blocklist_entries WHERE entry_type = 'device' AND value = 'device-bad'`).Scan(&entries); err != nil || entries != 1 {
		t.Fatalf("entries for the device = %d, err = %v, want 1", entries, err)
	}

	// A conflicting decision creates no entry.
	decided := withDevice("block-decided", "device-conflict")
	if rec := decide(decided, `{"decision":"approve"}`); rec.Code != http.StatusCreated {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}
	if rec := decide(decided, reject); rec.Code != http.StatusConflict || blocked("device-conflict") {
		t.Fatalf("conflicting rejection = %d %s, blocked = %v", rec.Code, rec.Body, blocked("device-conflict"))
	}

	// A purchase without a device is rejected and nothing is blocked.
	noDevice := insertDecision(t, tx, "block-no-device", "review", at, nil, `[]`)
	if rec := decide(noDevice, reject); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"device_blocked":false`) {
		t.Fatalf("rejection without a device = %d %s", rec.Code, rec.Body)
	}

	// The option only makes sense for a rejection.
	approve := withDevice("block-approve", "device-approve")
	if rec := decide(approve, `{"decision":"approve","block_device":true}`); rec.Code != http.StatusBadRequest || blocked("device-approve") {
		t.Fatalf("approval with the option = %d %s, blocked = %v", rec.Code, rec.Body, blocked("device-approve"))
	}
}

// TestRejectedDeviceBlocksNextScoredPurchase rejects a review with the device
// option and then scores a new purchase from that device: it is blocked, with
// no restart or cache in between.
func TestRejectedDeviceBlocksNextScoredPurchase(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewStore(tx), testAnalystKeys(t))
	score.RegisterRoutes(mux, score.NewHandler(rulesets.NewStore(tx), blocklist.NewStore(tx), score.NewStore(tx)), serviceTestKey)

	scoreFrom := func(txnID, device string) string {
		t.Helper()
		body := `{"transaction_id":"` + txnID + `","event_time":"2026-11-09T10:00:00Z","amount_minor":1999,"player_id":"score-player","card_token":"score-card","device_id":"` + device + `"}`
		req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(body))
		req.Header.Set(platform.APIKeyHeader, serviceTestKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("score %s: status %d body %s", txnID, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}

	const device = "device-e2e-reject"
	if got := scoreFrom("e2e-before", device); !strings.Contains(got, `"outcome":"allow"`) {
		t.Fatalf("before the rejection: %s, want allow", got)
	}

	id := insertDecision(t, tx, "e2e-review", "review", time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), nil, `[]`)
	if _, err := tx.Exec(ctx, `UPDATE purchases SET device_id = $2 WHERE id = $1`, id, device); err != nil {
		t.Fatalf("set device ID: %v", err)
	}
	rec := reviewRequest(mux, http.MethodPost, "/reviews/"+strconv.FormatInt(id, 10)+"/decision", queueTestKey,
		`{"decision":"reject","block_device":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reject: status %d body %s", rec.Code, rec.Body)
	}

	if got := scoreFrom("e2e-after", device); !strings.Contains(got, `"outcome":"block"`) {
		t.Fatalf("after the rejection: %s, want block", got)
	}
	if got := scoreFrom("e2e-other-device", "device-unrelated"); !strings.Contains(got, `"outcome":"allow"`) {
		t.Fatalf("a different device: %s, want allow", got)
	}
}
