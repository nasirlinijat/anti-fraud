package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats"
)

type fakeSource struct {
	sum      models.StatsSummary
	recent   models.RecentPipeline
	latest   int64
	err      error
	window   models.StatsWindow
	maxRules int
	after    int64
	limit    int
}

func (f *fakeSource) RecentPipeline(_ context.Context, after int64, limit int) (models.RecentPipeline, error) {
	f.after, f.limit = after, limit
	return f.recent, f.err
}

func (f *fakeSource) LatestDecisionID(context.Context) (int64, error) {
	return f.latest, f.err
}

func (f *fakeSource) Summary(_ context.Context, window models.StatsWindow, maxRules int) (models.StatsSummary, error) {
	f.window, f.maxRules = window, maxRules
	return f.sum, f.err
}

var statsNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func getStats(t *testing.T, src stats.Store, key, query string) *httptest.ResponseRecorder {
	t.Helper()
	return getStatsPath(t, src, key, "/stats"+query)
}

func getStatsPath(t *testing.T, src stats.Store, key, path string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewStatsHandler(stats.NewService(src))
	h.now = func() time.Time { return statsNow }
	mux := http.NewServeMux()
	RegisterStatsRoutes(mux, h, platform.Access{Sessions: consoleTestSessions})
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		setTestCredential(req, key)
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
		{"", "24h", statsNow.Add(-24 * time.Hour), time.Hour},
		{"?period=15m", "15m", statsNow.Add(-15 * time.Minute), time.Minute},
		{"?period=1h", "1h", statsNow.Add(-time.Hour), 5 * time.Minute},
		{"?period=7d", "7d", statsNow.Add(-7 * 24 * time.Hour), 6 * time.Hour},
		{"?period=all", "all", time.Unix(0, 0).UTC(), 0},
	}
	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			src := &fakeSource{sum: models.StatsSummary{TopRules: []models.RuleCount{}}}
			rec := getStats(t, src, testKey, tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
			}
			wantWindow := models.StatsWindow{Since: tt.wantSince, Until: statsNow, BucketWidth: tt.wantWidth}
			if src.window != wantWindow || src.maxRules != 10 {
				t.Errorf("asked for window %+v and %d rules, want %+v and %d rules", src.window, src.maxRules, wantWindow, 10)
			}
			var got statsResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Period != tt.period || !got.Since.Equal(tt.wantSince) || !got.GeneratedAt.Equal(statsNow) {
				t.Errorf("response period %s since %v at %v", got.Period, got.Since, got.GeneratedAt)
			}
		})
	}
}

func TestStatsResponseBody(t *testing.T) {
	p95 := int64(1234)
	src := &fakeSource{sum: models.StatsSummary{
		Outcomes:            models.OutcomeCounts{Allow: 8, Review: 1, Block: 1, Total: 10},
		BlocklistBlocks:     1,
		TopRules:            []models.RuleCount{{Name: "high_amount", Fired: 2, Points: 60}},
		ReviewQueue:         3,
		Model:               models.ModelEffects{Scored: 10, Flagged: 1, Cleared: 2},
		P95ScoringLatencyUs: &p95,
		BlockedValueMinor:   "18446744073709551614",
		DecisionSeries: models.DecisionSeries{BucketSeconds: 300, Buckets: []models.DecisionBucket{
			{Start: statsNow.Add(-time.Hour), Allow: 8, Review: 1, Block: 1},
		}},
	}}
	rec := getStats(t, src, testKey, "?period=1h")
	body := rec.Body.String()
	for _, want := range []string{
		`"outcomes":{"allow":8,"review":1,"block":1,"total":10}`,
		`"blocklist_blocks":1`,
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
	rec := getStats(t, &fakeSource{}, testKey, "?period=2h")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"period"`) {
		t.Fatalf("status %d body %s, want 400 naming period", rec.Code, rec.Body.String())
	}
}

func TestRecentPipelineResponseBody(t *testing.T) {
	src := &fakeSource{recent: models.RecentPipeline{
		NextAfter:       81234,
		Outcomes:        models.OutcomeCounts{Allow: 30, Review: 3, Block: 1, Total: 34},
		BlocklistBlocks: 0,
		Model:           models.PipelineModelEffects{Flagged: 1},
	}}
	rec := getStatsPath(t, src, testKey, "/stats/pipeline/recent?after=81200")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	want := `{"after":81200,"next_after":81234,"outcomes":{"allow":30,"review":3,"block":1,"total":34},"blocklist_blocks":0,"model":{"flagged":1,"cleared":0}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body %s, want %s", got, want)
	}
	if src.after != 81200 || src.limit != 5000 {
		t.Errorf("asked after %d with limit %d, want 81200 and 5000", src.after, src.limit)
	}
}

func TestRecentPipelineWithoutCursorStartsFromNow(t *testing.T) {
	rec := getStatsPath(t, &fakeSource{latest: 81234}, testKey, "/stats/pipeline/recent")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	want := `{"after":null,"next_after":81234,"outcomes":{"allow":0,"review":0,"block":0,"total":0},"blocklist_blocks":0,"model":{"flagged":0,"cleared":0}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body %s, want %s", got, want)
	}
}

func TestRecentPipelineRejectsAnInvalidCursor(t *testing.T) {
	for _, after := range []string{"-1", "abc", "", "1.5", "9223372036854775808"} {
		t.Run(after, func(t *testing.T) {
			rec := getStatsPath(t, &fakeSource{}, testKey, "/stats/pipeline/recent?after="+after)
			want := `"fields":{"after":"must be a non-negative integer"}`
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("status %d body %s, want 400 containing %s", rec.Code, rec.Body.String(), want)
			}
		})
	}
}

func TestRecentPipelineRequiresAPIKey(t *testing.T) {
	if rec := getStatsPath(t, &fakeSource{}, "", "/stats/pipeline/recent"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRecentPipelineInternalError(t *testing.T) {
	rec := getStatsPath(t, &fakeSource{err: errors.New("database is down")}, testKey, "/stats/pipeline/recent?after=1")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "database is down") {
		t.Fatalf("status %d body %s, want 500 without the internal error", rec.Code, rec.Body.String())
	}
}

func TestStatsRequiresAPIKey(t *testing.T) {
	if rec := getStats(t, &fakeSource{}, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestStatsInternalError(t *testing.T) {
	rec := getStats(t, &fakeSource{err: errors.New("database is down")}, testKey, "")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "database is down") {
		t.Fatalf("status %d body %s, want 500 without the internal error", rec.Code, rec.Body.String())
	}
}
