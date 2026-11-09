package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const queueTestKey = "queue-test-key"

type queueFunc func(context.Context, int, *queueCursor) ([]QueueItem, error)

func (fn queueFunc) queue(ctx context.Context, limit int, after *queueCursor) ([]QueueItem, error) {
	return fn(ctx, limit, after)
}

func requestQueue(store queueReader, target, key string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	RegisterRoutes(mux, store, queueTestKey)
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
	for _, tc := range []struct {
		name   string
		target string
		key    string
		status int
	}{
		{"missing key", "/reviews", "", http.StatusUnauthorized},
		{"wrong key", "/reviews?cursor=!", "wrong", http.StatusUnauthorized},
		{"empty cursor", "/reviews?cursor=", queueTestKey, http.StatusBadRequest},
		{"malformed cursor", "/reviews?cursor=!", queueTestKey, http.StatusBadRequest},
		{"repeated cursor", "/reviews?cursor=a&cursor=b", queueTestKey, http.StatusBadRequest},
		{"oversized cursor", "/reviews?cursor=" + strings.Repeat("a", 129), queueTestKey, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := queueFunc(func(context.Context, int, *queueCursor) ([]QueueItem, error) {
				t.Fatal("rejected request queried the store")
				return nil, nil
			})
			w := requestQueue(store, tc.target, tc.key)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status = %d, headers = %v, body = %s", w.Code, w.Header(), w.Body)
			}
		})
	}
}

func TestQueueSerializesReviewAndEmptyPage(t *testing.T) {
	zero := 0.0
	at := time.Date(2026, 11, 9, 10, 0, 0, 123456000, time.FixedZone("UTC+4", 4*60*60))
	store := queueFunc(func(_ context.Context, limit int, after *queueCursor) ([]QueueItem, error) {
		if limit != 101 {
			t.Fatalf("store limit = %d, want 101", limit)
		}
		if after != nil {
			if !after.decidedAt.Equal(at) || after.purchaseID != 9007199254740993 {
				t.Fatalf("decoded cursor = %+v", after)
			}
			return nil, nil
		}
		return []QueueItem{
			{
				PurchaseID: 9007199254740993, TransactionID: "first", PlayerID: "player",
				EventTime: at.Add(-time.Minute), DecidedAt: at, AmountMinor: 9223372036854775807, RuleScore: 60,
			},
			{
				PurchaseID: 9007199254740994, TransactionID: "second", ModelScore: &zero,
				EventTime: at, DecidedAt: at, FiredRules: []scoring.FiredRule{{Name: scoring.RuleHighAmount, Points: 30}},
			},
		}, nil
	})
	w := requestQueue(store, "/reviews", queueTestKey)
	page := readQueuePage(t, w)
	if len(page.Items) != 2 || page.NextCursor != nil {
		t.Fatalf("unexpected page: %+v", page)
	}
	first := page.Items[0]
	if first.PurchaseID != "9007199254740993" || first.AmountMinor != "9223372036854775807" ||
		first.EventTime.Format(time.RFC3339Nano) != "2026-11-09T05:59:00.123456Z" ||
		first.QueuedAt.Format(time.RFC3339Nano) != "2026-11-09T06:00:00.123456Z" ||
		first.RuleScore != 60 || first.PlayerID != "player" || first.TransactionID != "first" ||
		first.ModelScore != nil || first.FiredRules == nil || len(first.FiredRules) != 0 {
		t.Fatalf("unexpected first review: %+v", first)
	}
	second := page.Items[1]
	if second.ModelScore == nil || *second.ModelScore != 0 ||
		len(second.FiredRules) != 1 || second.FiredRules[0].Name != scoring.RuleHighAmount || second.FiredRules[0].Points != 30 {
		t.Fatalf("unexpected second review: %+v", second)
	}

	cursor := encodeCursor(queueCursor{decidedAt: at, purchaseID: 9007199254740993})
	w = requestQueue(store, "/reviews?cursor="+cursor, queueTestKey)
	readQueuePage(t, w)
	if got := strings.TrimSpace(w.Body.String()); got != `{"items":[],"next_cursor":null}` {
		t.Fatalf("empty page = %s", got)
	}
}

func TestQueueHidesStoreErrors(t *testing.T) {
	store := queueFunc(func(context.Context, int, *queueCursor) ([]QueueItem, error) {
		return nil, errors.New("private database details")
	})
	w := requestQueue(store, "/reviews", queueTestKey)
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
			insertDecision(t, tx, "page-allow", "allow", decidedAt, nil, `[]`)
			insertDecision(t, tx, "page-block", "block", decidedAt, nil, `[]`)
		}
		id := insertDecision(t, tx, "page-review-"+strconv.Itoa(i), "review", decidedAt, nil, `[]`)
		want = append(want, strconv.FormatInt(id, 10))
	}

	store := NewStore(tx)
	target := "/reviews"
	for start := 0; start < len(want); start += pageSize {
		page := readQueuePage(t, requestQueue(store, target, queueTestKey))
		count := min(pageSize, len(want)-start)
		if len(page.Items) < count || len(page.Items) > pageSize {
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
