package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	scoresvc "github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

type rejectUnexpectedReviewAccess struct{}

func (rejectUnexpectedReviewAccess) Queue(context.Context, int, models.QueueOrder, *models.QueueCursor, *string) ([]models.QueueItem, error) {
	panic("unexpected queue read")
}

func (rejectUnexpectedReviewAccess) Case(context.Context, int64) (models.CaseItem, error) {
	panic("unexpected case read")
}

func (rejectUnexpectedReviewAccess) Network(context.Context, int64) (models.CaseNetwork, error) {
	panic("unexpected network read")
}

func (rejectUnexpectedReviewAccess) NetworkPurchases(context.Context, int64, models.BlocklistType, string) (models.LinkedPurchases, error) {
	panic("unexpected linked purchases read")
}

func (rejectUnexpectedReviewAccess) LinkedReviews(context.Context, int64) (models.LinkedReviews, error) {
	panic("unexpected linked reviews read")
}

func (rejectUnexpectedReviewAccess) Resolve(context.Context, models.ReviewDecision) (models.Resolution, error) {
	panic("unexpected models.Resolution write")
}

func (rejectUnexpectedReviewAccess) Claim(context.Context, int64, string) (models.Claim, error) {
	panic("unexpected claim write")
}

func (rejectUnexpectedReviewAccess) Assignments(context.Context) ([]models.ReviewerWork, error) {
	panic("unexpected assignments read")
}

func (rejectUnexpectedReviewAccess) ReleaseClaim(context.Context, int64) (models.ReleaseStatus, error) {
	panic("unexpected claim release")
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
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(rejectUnexpectedReviewAccess{}), false), platform.Access{})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/reviews"},
		{http.MethodGet, "/reviews/1"},
		{http.MethodGet, "/reviews/1/network"},
		{http.MethodGet, "/reviews/1/network/purchases?anchor=card&value=player-1"},
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
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(rejectUnexpectedReviewAccess{}), false), platform.Access{Analysts: testAnalystKeys(t)})
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

// postDecision sends a review decision for the purchase.
func postDecision(mux *http.ServeMux, id int64, key, body string) *httptest.ResponseRecorder {
	return reviewRequest(mux, http.MethodPost, "/reviews/"+strconv.FormatInt(id, 10)+"/decision", key, body)
}

// reviewCounts are the decision counts the dashboard shows. It derives review
// outcomes and model effects from decisions, so resolving a review must
// change only the open review count.
type reviewCounts struct{ allowed, reviewed, blocked, scored, flagged, cleared int64 }

// reviewStats is what a review decision must leave unchanged, except for the
// queue length.
type reviewStats struct {
	queue    int64
	outcomes reviewCounts
	model    reviewCounts
}

func readReviewStats(t *testing.T, tx pgx.Tx, from, to time.Time) reviewStats {
	t.Helper()
	var queue int64
	var c reviewCounts
	err := tx.QueryRow(context.Background(), `
		SELECT
		    (SELECT count(*) FROM decisions d WHERE d.outcome = 'review'
		       AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)),
		    count(*) FILTER (WHERE d.outcome = 'allow'),
		    count(*) FILTER (WHERE d.outcome = 'review'),
		    count(*) FILTER (WHERE d.outcome = 'block'),
		    count(d.model_score),
		    count(*) FILTER (WHERE d.outcome = 'review' AND d.rule_score < r.review_threshold),
		    count(*) FILTER (WHERE d.outcome = 'allow' AND d.rule_score >= r.review_threshold)
		FROM decisions d
		JOIN rulesets r ON r.version = d.ruleset_version
		WHERE d.decided_at >= $1 AND d.decided_at < $2`, from, to,
	).Scan(&queue, &c.allowed, &c.reviewed, &c.blocked, &c.scored, &c.flagged, &c.cleared)
	if err != nil {
		t.Fatal(err)
	}
	outcomes, model := c, c
	outcomes.scored, outcomes.flagged, outcomes.cleared = 0, 0, 0
	model.allowed, model.reviewed, model.blocked = 0, 0, 0
	return reviewStats{queue: queue, outcomes: outcomes, model: model}
}

type wantDecision struct {
	id                int64
	analyst, decision string
}

// checkSavedDecision checks the response to a saved decision and the row
// stored for it.
func checkSavedDecision(t *testing.T, tx pgx.Tx, rec *httptest.ResponseRecorder, want wantDecision) {
	t.Helper()
	var saved decisionResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &saved) != nil ||
		saved.PurchaseID != strconv.FormatInt(want.id, 10) || saved.AnalystID != want.analyst ||
		saved.Decision != want.decision || saved.ResolvedAt.IsZero() {
		t.Fatalf("save response = %d %s", rec.Code, rec.Body)
	}
	var analystID, decision string
	var resolvedAt time.Time
	err := tx.QueryRow(context.Background(), "SELECT analyst_id, decision, resolved_at FROM review_resolutions WHERE purchase_id = $1", want.id).
		Scan(&analystID, &decision, &resolvedAt)
	if err != nil || analystID != want.analyst || decision != want.decision || !resolvedAt.Equal(saved.ResolvedAt) {
		t.Fatalf("stored models.Resolution = %q %q %v, err = %v", analystID, decision, resolvedAt, err)
	}
}

// checkScoreKept checks that approving the purchase left its saved score and
// outcome as they were.
func checkScoreKept(t *testing.T, tx pgx.Tx, purchaseID int64, score float64) {
	t.Helper()
	var modelVersion, originalOutcome, originalResolution string
	var modelScore float64
	err := tx.QueryRow(context.Background(), `
		SELECT d.model_version, d.outcome, d.model_score, r.decision
		FROM decisions d JOIN review_resolutions r USING (purchase_id)
		WHERE d.purchase_id = $1`, purchaseID).Scan(&modelVersion, &originalOutcome, &modelScore, &originalResolution)
	if err != nil || modelVersion != testModelVersion || originalOutcome != "review" ||
		modelScore != score || originalResolution != "approve" {
		t.Fatalf("saved score or models.Resolution was changed: %q %q %v %q, err = %v",
			modelVersion, originalOutcome, modelScore, originalResolution, err)
	}
}

func TestReviewDecisionsAgainstPostgres(t *testing.T) {
	tx := testTx(t)
	at := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	score := 0.7
	first := insertReviewDecision(t, tx, "resolution-first", "review", at, &score, `[]`)
	second := insertReviewDecision(t, tx, "resolution-second", "review", at, nil, `[]`)
	allowed := insertReviewDecision(t, tx, "resolution-allowed", "allow", at, nil, `[]`)
	mux := http.NewServeMux()
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(storage.NewReviewStore(tx)), false), platform.Access{Analysts: testAnalystKeys(t)})
	until := time.Now().UTC()
	before := readReviewStats(t, tx, at, until)
	caseURL := "/reviews/" + strconv.FormatInt(first, 10)
	if rec := reviewRequest(mux, http.MethodGet, caseURL, secondAnalystTestKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("pending case = %d %s", rec.Code, rec.Body)
	}
	submit := func(id int64, key, decision string) *httptest.ResponseRecorder {
		return postDecision(mux, id, key, `{"decision":"`+decision+`"}`)
	}
	for _, tc := range []struct {
		id                     int64
		key, analyst, decision string
	}{
		{first, queueTestKey, "first", "approve"},
		{second, secondAnalystTestKey, "second", "reject"},
	} {
		rec := submit(tc.id, tc.key, tc.decision)
		checkSavedDecision(t, tx, rec, wantDecision{id: tc.id, analyst: tc.analyst, decision: tc.decision})
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
	after := readReviewStats(t, tx, at, until)
	if after.queue != before.queue-2 || after.outcomes != before.outcomes || after.model != before.model {
		t.Fatalf("unexpected stats after Resolution: queue %d → %d, outcomes %+v → %+v, model %+v → %+v",
			before.queue, after.queue, before.outcomes, after.outcomes, before.model, after.model)
	}
	checkScoreKept(t, tx, first, score)
}

// insertDeviceReview adds a purchase awaiting review that was made from the
// device.
func insertDeviceReview(t *testing.T, tx pgx.Tx, txnID, device string) int64 {
	t.Helper()
	id := insertReviewDecision(t, tx, txnID, "review", time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), nil, `[]`)
	if _, err := tx.Exec(context.Background(), `UPDATE purchases SET device_id = $2 WHERE id = $1`, id, device); err != nil {
		t.Fatalf("set device ID: %v", err)
	}
	return id
}

func deviceBlocked(t *testing.T, tx pgx.Tx, device string) bool {
	t.Helper()
	_, found, err := storage.NewBlocklistStore(tx).Match(context.Background(), models.BlocklistSubject{CardToken: "none", PlayerID: "none", DeviceID: &device})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestRejectCanBlockTheDevice(t *testing.T) {
	tx := testTx(t)
	mux := http.NewServeMux()
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(storage.NewReviewStore(tx)), false), platform.Access{Analysts: testAnalystKeys(t)})
	decide := func(id int64, body string) *httptest.ResponseRecorder {
		return postDecision(mux, id, queueTestKey, body)
	}
	blocked := func(device string) bool { return deviceBlocked(t, tx, device) }
	const reject = `{"decision":"reject","block_device":true}`

	// A rejection without the option leaves the device alone.
	plain := insertDeviceReview(t, tx, "block-plain", "device-plain")
	if rec := decide(plain, `{"decision":"reject"}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"device_blocked":false`) || blocked("device-plain") {
		t.Fatalf("plain rejection = %d %s, blocked = %v", rec.Code, rec.Body, blocked("device-plain"))
	}

	// With the option, the rejection and the entry are saved together, and the next purchase from the device is blocked.
	first := insertDeviceReview(t, tx, "block-first", "device-bad")
	if rec := decide(first, reject); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"device_blocked":true`) || !blocked("device-bad") {
		t.Fatalf("rejection with the option = %d %s, blocked = %v", rec.Code, rec.Body, blocked("device-bad"))
	}

	// A device that is already blocked adds no second entry, and the rejection still succeeds.
	again := insertDeviceReview(t, tx, "block-again", "device-bad")
	if rec := decide(again, reject); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"device_blocked":false`) {
		t.Fatalf("rejection of an already blocked device = %d %s", rec.Code, rec.Body)
	}
	if entries := countRows(t, tx, `SELECT count(*) FROM blocklist_entries WHERE entry_type = 'device' AND value = 'device-bad'`); entries != 1 {
		t.Fatalf("entries for the device = %d, want 1", entries)
	}
	if audited := countRows(t, tx, `
		SELECT count(*) FROM configuration_audit_events
		WHERE action = 'blocklist.add' AND actor_kind = 'analyst' AND actor_id = 'first'
		  AND details->>'value' = 'device-bad'
	`); audited != 1 {
		t.Fatalf("audit events for the device = %d, want 1", audited)
	}

	// A conflicting decision creates no entry.
	decided := insertDeviceReview(t, tx, "block-decided", "device-conflict")
	if rec := decide(decided, `{"decision":"approve"}`); rec.Code != http.StatusCreated {
		t.Fatalf("approve = %d %s", rec.Code, rec.Body)
	}
	if rec := decide(decided, reject); rec.Code != http.StatusConflict || blocked("device-conflict") {
		t.Fatalf("conflicting rejection = %d %s, blocked = %v", rec.Code, rec.Body, blocked("device-conflict"))
	}

	// A purchase without a device is rejected and nothing is blocked.
	noDevice := insertReviewDecision(t, tx, "block-no-device", "review", time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), nil, `[]`)
	if rec := decide(noDevice, reject); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"device_blocked":false`) {
		t.Fatalf("rejection without a device = %d %s", rec.Code, rec.Body)
	}

	// The option only makes sense for a rejection.
	approve := insertDeviceReview(t, tx, "block-approve", "device-approve")
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
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(storage.NewReviewStore(tx)), false), platform.Access{Analysts: testAnalystKeys(t)})
	RegisterScoreRoutes(mux, NewScoreHandler(scoresvc.NewService(storage.NewRulesetStore(tx), storage.NewBlocklistStore(tx), storage.NewPurchaseStore(tx))), serviceTestKey)

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

	id := insertReviewDecision(t, tx, "e2e-review", "review", time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), nil, `[]`)
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

const testModelVersion = "lgbm-test"

// insertDecision adds a purchase and its decision. The decision times are in
// 2000 so the test rows come before any other rows in the queue.
func insertReviewDecision(t *testing.T, tx pgx.Tx, txnID, outcome string, decidedAt time.Time, modelScore *float64, firedRules string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token)
		VALUES ($1, '2000-01-01 08:00:00+00', 2500, 'player-' || $1, 'card-1')
		RETURNING id`, txnID).Scan(&id)
	if err != nil {
		t.Fatalf("insert purchase %s: %v", txnID, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, model_score, model_version, decided_at)
		VALUES ($1, 1, $2, 60, $3, '{}', $4, CASE WHEN $4::float8 IS NULL THEN NULL ELSE $6 END, $5)`,
		id, outcome, firedRules, modelScore, decidedAt, testModelVersion)
	if err != nil {
		t.Fatalf("insert decision for %s: %v", txnID, err)
	}
	return id
}
