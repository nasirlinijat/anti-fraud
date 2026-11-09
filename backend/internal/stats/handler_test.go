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
	since    time.Time
	maxRules int
}

func (f *fakeSource) Summary(_ context.Context, since time.Time, maxRules int) (Summary, error) {
	f.since, f.maxRules = since, maxRules
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
	}{
		{"", "24h", now.Add(-24 * time.Hour)},
		{"?period=15m", "15m", now.Add(-15 * time.Minute)},
		{"?period=1h", "1h", now.Add(-time.Hour)},
		{"?period=7d", "7d", now.Add(-7 * 24 * time.Hour)},
		{"?period=all", "all", time.Unix(0, 0).UTC()},
	}
	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			src := &fakeSource{sum: Summary{TopRules: []RuleCount{}}}
			rec := get(t, src, testKey, tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			if !src.since.Equal(tt.wantSince) || src.maxRules != maxRules {
				t.Errorf("asked since %v for %d rules, want %v for %d", src.since, src.maxRules, tt.wantSince, maxRules)
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
	src := &fakeSource{sum: Summary{
		Outcomes:    Outcomes{Allow: 8, Review: 1, Block: 1, Total: 10},
		TopRules:    []RuleCount{{Name: "high_amount", Fired: 2, Points: 60}},
		ReviewQueue: 3,
		Model:       ModelEffects{Scored: 10, Flagged: 1, Cleared: 2},
	}}
	rec := get(t, src, testKey, "?period=1h")
	body := rec.Body.String()
	for _, want := range []string{
		`"outcomes":{"allow":8,"review":1,"block":1,"total":10}`,
		`"top_rules":[{"name":"high_amount","fired":2,"points":60}]`,
		`"review_queue":3`,
		`"model":{"scored":10,"flagged":1,"cleared":2}`,
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
