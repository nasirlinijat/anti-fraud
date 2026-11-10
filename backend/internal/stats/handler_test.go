package stats

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "test-key"

type fakeSource struct {
	sum      Summary
	err      error
	window   Window
	maxRules int
}

func (f *fakeSource) Summary(_ context.Context, window Window, maxRules int) (Summary, error) {
	f.window, f.maxRules = window, maxRules
	return f.sum, f.err
}

var now = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func get(t *testing.T, src Source, key, query string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(src)
	h.now = func() time.Time { return now }
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, testKey)
	req := httptest.NewRequest(http.MethodGet, "/stats"+query, nil)
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestStatsPeriods(t *testing.T) {
	tests := []struct {
		query     string
		period    string
		wantSince time.Time
		wantWidth time.Duration
	}{
		{"", "24h", now.Add(-24 * time.Hour), time.Hour},
		{"?period=15m", "15m", now.Add(-15 * time.Minute), time.Minute},
		{"?period=1h", "1h", now.Add(-time.Hour), 5 * time.Minute},
		{"?period=7d", "7d", now.Add(-7 * 24 * time.Hour), 6 * time.Hour},
		{"?period=all", "all", time.Unix(0, 0).UTC(), 0},
	}
	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			src := &fakeSource{sum: Summary{TopRules: []RuleCount{}}}
			rec := get(t, src, testKey, tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			wantWindow := Window{Since: tt.wantSince, Until: now, BucketWidth: tt.wantWidth}
			if src.window != wantWindow || src.maxRules != maxRules {
				t.Errorf("asked for window %+v and %d rules, want %+v and %d rules", src.window, src.maxRules, wantWindow, maxRules)
			}
			var got response
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Period != tt.period || !got.Since.Equal(tt.wantSince) || !got.GeneratedAt.Equal(now) {
				t.Errorf("response period %s since %v at %v", got.Period, got.Since, got.GeneratedAt)
			}
		})
	}
}

func TestStatsResponseBody(t *testing.T) {
	p95 := int64(1234)
	src := &fakeSource{sum: Summary{
		Outcomes:            Outcomes{Allow: 8, Review: 1, Block: 1, Total: 10},
		TopRules:            []RuleCount{{Name: "high_amount", Fired: 2, Points: 60}},
		ReviewQueue:         3,
		Model:               ModelEffects{Scored: 10, Flagged: 1, Cleared: 2},
		P95ScoringLatencyUs: &p95,
		BlockedValueMinor:   "18446744073709551614",
		DecisionSeries: DecisionSeries{BucketSeconds: 300, Buckets: []DecisionBucket{
			{Start: now.Add(-time.Hour), Allow: 8, Review: 1, Block: 1},
		}},
	}}
	rec := get(t, src, testKey, "?period=1h")
	body := rec.Body.String()
	for _, want := range []string{
		`"outcomes":{"allow":8,"review":1,"block":1,"total":10}`,
		`"top_rules":[{"name":"high_amount","fired":2,"points":60}]`,
		`"review_queue":3`,
		`"model":{"scored":10,"flagged":1,"cleared":2}`,
		`"p95_scoring_latency_us":1234`,
		`"blocked_value_minor":"18446744073709551614"`,
		`"decision_series":{"bucket_seconds":300,"buckets":[{"start":"2026-11-09T11:00:00Z","allow":8,"review":1,"block":1}]}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s, want it to contain %s", body, want)
		}
	}
}

func TestStatsRejectsUnknownPeriod(t *testing.T) {
	rec := get(t, &fakeSource{}, testKey, "?period=2h")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"period"`) {
		t.Fatalf("status %d body %s, want 400 naming period", rec.Code, rec.Body.String())
	}
}

func TestStatsRequiresAPIKey(t *testing.T) {
	if rec := get(t, &fakeSource{}, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestStatsInternalError(t *testing.T) {
	rec := get(t, &fakeSource{err: errors.New("database is down")}, testKey, "")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "database is down") {
		t.Fatalf("status %d body %s, want 500 without the internal error", rec.Code, rec.Body.String())
	}
}
