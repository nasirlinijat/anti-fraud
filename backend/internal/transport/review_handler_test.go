package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

const queueTestKey = "queue-test-key"

type queueFunc func(context.Context, int, models.QueueOrder, *models.QueueCursor) ([]models.QueueItem, error)

func (fn queueFunc) Queue(ctx context.Context, limit int, order models.QueueOrder, after *models.QueueCursor, _ *string) ([]models.QueueItem, error) {
	return fn(ctx, limit, order, after)
}

type caseFunc func(context.Context, int64) (models.CaseItem, error)

func (fn caseFunc) Case(ctx context.Context, id int64) (models.CaseItem, error) {
	return fn(ctx, id)
}

type networkFunc func(context.Context, int64) (models.CaseNetwork, error)

func (fn networkFunc) Network(ctx context.Context, id int64) (models.CaseNetwork, error) {
	return fn(ctx, id)
}

// Tests provide only the read method the request is expected to call.
type queueReader interface {
	Queue(context.Context, int, models.QueueOrder, *models.QueueCursor, *string) ([]models.QueueItem, error)
}

type caseReader interface {
	Case(context.Context, int64) (models.CaseItem, error)
}

type networkReader interface {
	Network(context.Context, int64) (models.CaseNetwork, error)
}

type fakeReviewReads struct {
	queueReader
	caseReader
	networkReader
}

func (fakeReviewReads) Resolve(context.Context, models.ReviewDecision) (models.Resolution, error) {
	panic("unexpected decision request")
}

func (fakeReviewReads) Claim(context.Context, int64, string) (models.Claim, error) {
	panic("unexpected claim request")
}

func (fakeReviewReads) Assignments(context.Context) ([]models.ReviewerWork, error) {
	panic("unexpected assignments request")
}

func (fakeReviewReads) ReleaseClaim(context.Context, int64) (models.ReleaseStatus, error) {
	panic("unexpected release request")
}

const secondAnalystTestKey = "second-queue-test-key"
const serviceTestKey = "service-test-key"

func testAnalystKeys(t *testing.T) platform.AnalystKeys {
	t.Helper()
	keys, err := platform.ParseAnalystKeys(`[{"analyst_id":"first","key":"`+queueTestKey+`"},{"analyst_id":"second","key":"`+secondAnalystTestKey+`"}]`, serviceTestKey)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func requestQueue(t *testing.T, store queueReader, target, key string) *httptest.ResponseRecorder {
	t.Helper()
	return requestReviewRead(t, fakeReviewReads{queueReader: store}, target, key)
}

func requestCase(t *testing.T, store caseReader, target, key string) *httptest.ResponseRecorder {
	t.Helper()
	return requestReviewRead(t, fakeReviewReads{caseReader: store}, target, key)
}

func requestNetwork(t *testing.T, store networkReader, target, key string) *httptest.ResponseRecorder {
	t.Helper()
	return requestReviewRead(t, fakeReviewReads{networkReader: store}, target, key)
}

func requestReviewRead(t *testing.T, store review.Store, target, key string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(store), true), platform.Access{Analysts: testAnalystKeys(t)})
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if key != "" {
		r.Header.Set(platform.APIKeyHeader, key)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func readQueuePage(t *testing.T, w *httptest.ResponseRecorder) queueResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if cache := w.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q", cache)
	}
	var page queueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode queue response: %v", err)
	}
	return page
}

func TestQueueRejectsUnauthorizedAndInvalidCursors(t *testing.T) {
	ruleCursor := encodeCursor(models.QueueByRuleScore, models.QueueCursor{DecidedAt: time.Now(), PurchaseID: 1, ScoreKey: -60})
	for _, tc := range []struct {
		name   string
		target string
		key    string
		status int
	}{
		{"missing key", "/reviews", "", http.StatusUnauthorized},
		{"wrong key", "/reviews?cursor=!", "wrong", http.StatusUnauthorized},
		{"service key", "/reviews", serviceTestKey, http.StatusUnauthorized},
		{"unknown sort", "/reviews?sort=unknown", queueTestKey, http.StatusBadRequest},
		{"cursor from another sort", "/reviews?sort=model_score&cursor=" + ruleCursor, queueTestKey, http.StatusBadRequest},
		{"repeated sort", "/reviews?sort=rule_score&sort=model_score", queueTestKey, http.StatusBadRequest},
		{"empty cursor", "/reviews?cursor=", queueTestKey, http.StatusBadRequest},
		{"malformed cursor", "/reviews?cursor=!", queueTestKey, http.StatusBadRequest},
		{"repeated cursor", "/reviews?cursor=a&cursor=b", queueTestKey, http.StatusBadRequest},
		{"oversized cursor", "/reviews?cursor=" + strings.Repeat("a", 129), queueTestKey, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := queueFunc(func(context.Context, int, models.QueueOrder, *models.QueueCursor) ([]models.QueueItem, error) {
				t.Fatal("rejected request queried the store")
				return nil, nil
			})
			w := requestQueue(t, store, tc.target, tc.key)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
			}
		})
	}
}

func TestQueueSerializesReviewAndEmptyPage(t *testing.T) {
	zero := 0.0
	version := "lgbm-ieee-v5-combined"
	at := time.Date(2026, 11, 9, 10, 0, 0, 123456000, time.FixedZone("UTC+4", 4*60*60))
	store := queueFunc(func(_ context.Context, limit int, order models.QueueOrder, after *models.QueueCursor) ([]models.QueueItem, error) {
		if limit != 101 || order != models.QueueByRuleScore {
			t.Fatalf("unexpected queue request: limit=%d order=%s", limit, order)
		}
		if after != nil {
			if !after.DecidedAt.Equal(at) || after.PurchaseID != 9007199254740993 || after.ScoreKey != -60 {
				t.Fatalf("decoded cursor = %+v", after)
			}
			return nil, nil
		}
		return []models.QueueItem{
			{
				PurchaseID: 9007199254740993, TransactionID: "first", PlayerID: "player",
				EventTime: at.Add(-time.Minute), DecidedAt: at, AmountMinor: 9223372036854775807, RuleScore: 60,
			},
			{
				PurchaseID: 9007199254740994, TransactionID: "second", ModelScore: &zero, ModelVersion: &version,
				EventTime: at, DecidedAt: at, FiredRules: []scoring.FiredRule{{Name: scoring.RuleHighAmount, Points: 30}},
			},
		}, nil
	})
	w := requestQueue(t, store, "/reviews", queueTestKey)
	page := readQueuePage(t, w)
	if len(page.Items) != 2 || page.NextCursor != nil || !page.ModelEnabled {
		t.Fatalf("unexpected page: %+v", page)
	}
	first := page.Items[0]
	if first.PurchaseID != "9007199254740993" || first.AmountMinor != "9223372036854775807" ||
		first.EventTime.Format(time.RFC3339Nano) != "2026-11-09T05:59:00.123456Z" ||
		first.QueuedAt.Format(time.RFC3339Nano) != "2026-11-09T06:00:00.123456Z" ||
		first.RuleScore != 60 || first.PlayerID != "player" || first.DeviceID != nil || first.TransactionID != "first" ||
		first.ModelScore != nil || first.ModelVersion != nil || first.FiredRules == nil || len(first.FiredRules) != 0 {
		t.Fatalf("unexpected first review: %+v", first)
	}
	second := page.Items[1]
	if second.ModelScore == nil || *second.ModelScore != 0 || second.ModelVersion == nil || *second.ModelVersion != version ||
		len(second.FiredRules) != 1 || second.FiredRules[0].Name != scoring.RuleHighAmount || second.FiredRules[0].Points != 30 {
		t.Fatalf("unexpected second review: %+v", second)
	}

	cursor := encodeCursor(models.QueueByRuleScore, models.QueueCursor{DecidedAt: at, PurchaseID: 9007199254740993, ScoreKey: -60})
	w = requestQueue(t, store, "/reviews?cursor="+cursor, queueTestKey)
	readQueuePage(t, w)
	if got := strings.TrimSpace(w.Body.String()); got != `{"items":[],"next_cursor":null,"model_enabled":true}` {
		t.Fatalf("empty page = %s", got)
	}
}

func TestQueueHidesStoreErrors(t *testing.T) {
	store := queueFunc(func(context.Context, int, models.QueueOrder, *models.QueueCursor) ([]models.QueueItem, error) {
		return nil, errors.New("private database details")
	})
	w := requestQueue(t, store, "/reviews", queueTestKey)
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
}

func TestCaseRejectsUnauthorizedAndInvalidIDs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		key    string
		status int
	}{
		{"missing key", "/reviews/1", "", http.StatusUnauthorized},
		{"wrong key", "/reviews/1", "wrong", http.StatusUnauthorized},
		{"service key", "/reviews/1", serviceTestKey, http.StatusUnauthorized},
		{"not a number", "/reviews/abc", queueTestKey, http.StatusBadRequest},
		{"zero", "/reviews/0", queueTestKey, http.StatusBadRequest},
		{"negative", "/reviews/-5", queueTestKey, http.StatusBadRequest},
		{"too large", "/reviews/9223372036854775808", queueTestKey, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := caseFunc(func(context.Context, int64) (models.CaseItem, error) {
				t.Fatal("rejected request queried the store")
				return models.CaseItem{}, nil
			})
			w := requestCase(t, store, tc.target, tc.key)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
			}
		})
	}
}

func TestCaseSerializesEvidence(t *testing.T) {
	score := 0.42
	version := "lgbm-ieee-v5-combined"
	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	store := caseFunc(func(_ context.Context, id int64) (models.CaseItem, error) {
		if id != 9007199254740993 {
			t.Fatalf("store ID = %d", id)
		}
		return models.CaseItem{
			QueueItem: models.QueueItem{
				PurchaseID: id, TransactionID: "txn", PlayerID: "player", EventTime: at.Add(-time.Minute), DecidedAt: at,
				AmountMinor: 2500, RuleScore: 70, ModelScore: &score, ModelVersion: &version,
				FiredRules: []scoring.FiredRule{{Name: scoring.RuleHighAmount, Points: 30}, {Name: scoring.RuleCardVelocity, Points: 40}},
			},
			RulesetVersion: 3,
			PlayerPurchases: []models.PlayerPurchase{
				{TransactionID: "prev", EventTime: at.Add(-2 * time.Minute), AmountMinor: 1200, Outcome: scoring.OutcomeAllow},
			},
			DeviceActivity: &models.DeviceActivity{Purchases: 2, DistinctCards: 1, LastSeenAt: at.Add(-time.Minute)},
		}, nil
	})
	w := requestCase(t, store, "/reviews/9007199254740993", queueTestKey)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
	}
	var got caseResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode case response: %v", err)
	}
	if got.PurchaseID != "9007199254740993" || got.TransactionID != "txn" || got.AmountMinor != "2500" ||
		got.PlayerID != "player" || got.RuleScore != 70 || got.RulesetVersion != 3 ||
		got.EventTime.Format(time.RFC3339) != "2026-11-09T09:59:00Z" || got.QueuedAt.Format(time.RFC3339) != "2026-11-09T10:00:00Z" ||
		got.ModelScore == nil || *got.ModelScore != score || got.ModelVersion == nil || *got.ModelVersion != version ||
		len(got.FiredRules) != 2 || got.FiredRules[1].Name != scoring.RuleCardVelocity || got.FiredRules[1].Points != 40 ||
		len(got.PlayerPurchases) != 1 || got.PlayerPurchases[0].TransactionID != "prev" || got.PlayerPurchases[0].AmountMinor != "1200" ||
		got.PlayerPurchases[0].Outcome != scoring.OutcomeAllow || got.PlayerPurchases[0].EventTime.Format(time.RFC3339) != "2026-11-09T09:58:00Z" ||
		got.DeviceActivity == nil || got.DeviceActivity.Purchases != 2 || got.DeviceActivity.DistinctCards != 1 ||
		got.DeviceActivity.LastSeenAt.Format(time.RFC3339) != "2026-11-09T09:59:00Z" {
		t.Fatalf("unexpected case: %+v", got)
	}
}

func TestCaseShowsDeviceID(t *testing.T) {
	device := "device-1"
	for name, want := range map[string]*string{"with device": &device, "without device": nil} {
		t.Run(name, func(t *testing.T) {
			store := caseFunc(func(_ context.Context, id int64) (models.CaseItem, error) {
				return models.CaseItem{QueueItem: models.QueueItem{PurchaseID: id, DeviceID: want}, RulesetVersion: 1}, nil
			})
			var body map[string]any
			w := requestCase(t, store, "/reviews/7", queueTestKey)
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
				t.Fatalf("status = %d, err = %v, body = %s", w.Code, err, w.Body)
			}
			got, present := body["device_id"]
			if !present || (want == nil && got != nil) || (want != nil && got != *want) {
				t.Fatalf("device_id = %v (present %v), want %v", got, present, want)
			}
		})
	}
}

func TestCaseWithoutModelScoreOrRules(t *testing.T) {
	store := caseFunc(func(_ context.Context, id int64) (models.CaseItem, error) {
		return models.CaseItem{QueueItem: models.QueueItem{PurchaseID: id, TransactionID: "txn"}, RulesetVersion: 1}, nil
	})
	w := requestCase(t, store, "/reviews/7", queueTestKey)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status = %d, err = %v, body = %s", w.Code, err, w.Body)
	}
	rules, isList := body["fired_rules"].([]any)
	purchases, isPurchasesList := body["player_purchases"].([]any)
	events, isEventsList := body["account_events"].([]any)
	if body["model_score"] != nil || body["model_version"] != nil || !isList || len(rules) != 0 ||
		!isPurchasesList || len(purchases) != 0 || body["device_activity"] != nil || !isEventsList || len(events) != 0 {
		t.Fatalf("expected null model fields and empty history: %s", w.Body)
	}
}

func TestCaseListsThePlayersAccountEvents(t *testing.T) {
	device, country := "device-9", "TR"
	decidedAt := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	store := caseFunc(func(_ context.Context, id int64) (models.CaseItem, error) {
		return models.CaseItem{QueueItem: models.QueueItem{PurchaseID: id, DecidedAt: decidedAt}, RulesetVersion: 1, AccountEvents: []models.AccountEvent{
			{EventID: "evt-2", PlayerID: "player", EventType: models.AccountEventLogin,
				EventTime: time.Date(2026, 11, 9, 13, 30, 0, 0, time.FixedZone("AZT", 4*3600)), DeviceID: &device, IPCountry: &country,
				ReceivedAt: decidedAt.Add(time.Hour)},
			{EventID: "evt-1", PlayerID: "player", EventType: models.AccountEventPasswordReset, EventTime: time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC),
				ReceivedAt: decidedAt.Add(-time.Minute)},
		}}, nil
	})

	w := requestCase(t, store, "/reviews/7", queueTestKey)

	wantResponse(t, w, http.StatusOK,
		`"account_events":[{"event_id":"evt-2","event_type":"login","event_time":"2026-11-09T09:30:00Z","device_id":"device-9","ip_country":"TR",`+
			`"received_at":"2026-11-09T11:00:00Z","recorded_after_decision":true},`+
			`{"event_id":"evt-1","event_type":"password_reset","event_time":"2026-11-09T09:00:00Z","device_id":null,"ip_country":null,`+
			`"received_at":"2026-11-09T09:59:00Z","recorded_after_decision":false}]`)
}

func TestCaseNotFoundAndStoreErrors(t *testing.T) {
	notFound := caseFunc(func(context.Context, int64) (models.CaseItem, error) {
		return models.CaseItem{}, models.ErrReviewNotFound
	})
	w := requestCase(t, notFound, "/reviews/5", queueTestKey)
	if w.Code != http.StatusNotFound || strings.TrimSpace(w.Body.String()) != `{"error":"review case not found"}` {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	failing := caseFunc(func(context.Context, int64) (models.CaseItem, error) {
		return models.CaseItem{}, errors.New("private database details")
	})
	w = requestCase(t, failing, "/reviews/5", queueTestKey)
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
}

func TestNetworkRejectsUnauthorizedAndInvalidIDs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		key    string
		status int
	}{
		{"missing key", "/reviews/1/network", "", http.StatusUnauthorized},
		{"wrong key", "/reviews/1/network", "wrong", http.StatusUnauthorized},
		{"service key", "/reviews/1/network", serviceTestKey, http.StatusUnauthorized},
		{"not a number", "/reviews/abc/network", queueTestKey, http.StatusBadRequest},
		{"zero", "/reviews/0/network", queueTestKey, http.StatusBadRequest},
		{"negative", "/reviews/-5/network", queueTestKey, http.StatusBadRequest},
		{"too large", "/reviews/9223372036854775808/network", queueTestKey, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := networkFunc(func(context.Context, int64) (models.CaseNetwork, error) {
				t.Fatal("rejected request queried the store")
				return models.CaseNetwork{}, nil
			})
			w := requestNetwork(t, store, tc.target, tc.key)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
			}
		})
	}
}

func TestNetworkSerializesAnchorsAndLinks(t *testing.T) {
	store := networkFunc(func(_ context.Context, id int64) (models.CaseNetwork, error) {
		if id != 9007199254740993 {
			t.Fatalf("store ID = %d", id)
		}
		return models.CaseNetwork{WindowDays: 30, Anchors: []models.NetworkAnchor{
			{Type: models.BlocklistDevice, Value: "device-1", Blocklisted: true, LinkedTotal: 31, Linked: []models.LinkedEntity{
				{Type: models.BlocklistPlayer, Value: "player-2", Purchases: 3, Status: models.NetworkRejected},
			}},
			{Type: models.BlocklistCard, Value: "card-1"},
		}}, nil
	})

	w := requestNetwork(t, store, "/reviews/9007199254740993/network", queueTestKey)

	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
	}
	want := `{"window_days":30,"anchors":[` +
		`{"type":"device","value":"device-1","blocklisted":true,"linked_total":31,"linked":[{"type":"player","value":"player-2","purchases":3,"status":"rejected"}]},` +
		`{"type":"card","value":"card-1","blocklisted":false,"linked_total":0,"linked":[]}]}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}
}

func TestNetworkNotFoundAndStoreErrors(t *testing.T) {
	notFound := networkFunc(func(context.Context, int64) (models.CaseNetwork, error) {
		return models.CaseNetwork{}, models.ErrReviewNotFound
	})
	w := requestNetwork(t, notFound, "/reviews/5/network", queueTestKey)
	if w.Code != http.StatusNotFound || strings.TrimSpace(w.Body.String()) != `{"error":"review case not found"}` {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	failing := networkFunc(func(context.Context, int64) (models.CaseNetwork, error) {
		return models.CaseNetwork{}, errors.New("private database details")
	})
	w = requestNetwork(t, failing, "/reviews/5/network", queueTestKey)
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != `{"error":"internal error"}` {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
}

func TestQueuePaginationAgainstPostgres(t *testing.T) {
	tx := testTx(t)
	at := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	want := make([]string, 0, 201)
	for i := range 201 {
		decidedAt := at.Add(time.Duration(i/3) * time.Microsecond)
		if i == 99 {
			insertReviewDecision(t, tx, "page-allow", "allow", decidedAt, nil, `[]`)
			insertReviewDecision(t, tx, "page-block", "block", decidedAt, nil, `[]`)
		}
		id := insertReviewDecision(t, tx, "page-review-"+strconv.Itoa(i), "review", decidedAt, nil, `[]`)
		want = append(want, strconv.FormatInt(id, 10))
	}

	store := storage.NewReviewStore(tx)
	target := "/reviews"
	for start := 0; start < len(want); start += review.PageSize {
		page := readQueuePage(t, requestQueue(t, store, target, queueTestKey))
		count := min(review.PageSize, len(want)-start)
		if len(page.Items) < count || len(page.Items) > review.PageSize {
			t.Fatalf("page starting at %d returned %d rows, expected at least %d", start, len(page.Items), count)
		}
		for i, id := range want[start : start+count] {
			if got := page.Items[i].PurchaseID; got != id {
				t.Fatalf("row %d purchase ID = %s, want %s", start+i, got, id)
			}
		}
		if start+count < len(want) {
			if page.NextCursor == nil {
				t.Fatalf("missing next cursor after row %d", start+count)
			}
			target = "/reviews?cursor=" + *page.NextCursor
		}
	}
}

func TestQueuePagesInBothScoreOrders(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	at := time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC)
	high, low := 0.9, 0.0
	insert := func(name string, rule int, model *float64, time time.Time) int64 {
		t.Helper()
		id := insertReviewDecision(t, tx, name, "review", time, model, "[]")
		if _, err := tx.Exec(ctx, "UPDATE decisions SET rule_score = $1 WHERE purchase_id = $2", rule, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := insert("sort-a", 70, nil, at)
	b := insert("sort-b", 50, &high, at)
	c := insert("sort-c", 70, &high, at)
	d := insert("sort-d", 90, &low, at.Add(time.Minute))
	e := insert("sort-e", 70, nil, at)
	f := insert("sort-f", 70, nil, at.Add(time.Minute))

	store := storage.NewReviewStore(tx)
	for _, tc := range []struct {
		order models.QueueOrder
		want  []int64
	}{
		{models.QueueByRuleScore, []int64{d, a, c, e, f, b}},
		{models.QueueByModelScore, []int64{b, c, d, a, e, f}},
	} {
		t.Run(string(tc.order), func(t *testing.T) {
			var cursor *models.QueueCursor
			var got []int64
			for range tc.want {
				rows, err := store.Queue(ctx, 1, tc.order, cursor, nil)
				if err != nil || len(rows) != 1 {
					t.Fatalf("queue page: rows=%v err=%v", rows, err)
				}
				got = append(got, rows[0].PurchaseID)
				last := review.CursorFor(tc.order, rows[0])
				cursor, err = parseCursor([]string{encodeCursor(tc.order, last)}, tc.order)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("purchase order = %v, want %v", got, tc.want)
			}
		})
	}
}
