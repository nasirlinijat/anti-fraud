package stats

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

type fakeQuality struct {
	q                  Quality
	err                error
	source, run, after string
	limit              int
}

func (f *fakeQuality) Quality(_ context.Context, source, run string) (Quality, error) {
	f.source, f.run = source, run
	return f.q, f.err
}
func (f *fakeQuality) Runs(_ context.Context, source, after string, limit int) (QualityRuns, error) {
	f.source, f.after, f.limit = source, after, limit
	return QualityRuns{Source: source, RunIDs: []string{}}, f.err
}
func qualityGet(src QualitySource, path string, keys ...string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	RegisterQualityRoutes(mux, src, platform.Access{Sessions: qualitySessions{}})
	req := httptest.NewRequest("GET", path, nil)
	for _, key := range keys {
		req.Header.Add("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestQualityValidationAndAuthentication(t *testing.T) {
	for _, path := range []string{"/stats/quality?run_id=r", "/stats/quality/runs"} {
		for _, keys := range [][]string{nil, {"wrong"}, {"quality-key", "quality-key"}} {
			if rec := qualityGet(&fakeQuality{}, path, keys...); rec.Code != 401 {
				t.Fatalf("%s: %d", path, rec.Code)
			}
		}
	}
	for _, query := range []string{"", "?run_id=", "?run_id=r&source=unknown", "?run_id=r&source=", "?run_id=a&run_id=b", "?run_id=r&period=all"} {
		if rec := qualityGet(&fakeQuality{}, "/stats/quality"+query, "quality-key"); rec.Code != 400 {
			t.Fatalf("%s: %d", query, rec.Code)
		}
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=x", "?limit=1&limit=2", "?source=bad", "?cursor=x"} {
		if rec := qualityGet(&fakeQuality{}, "/stats/quality/runs"+query, "quality-key"); rec.Code != 400 {
			t.Fatalf("%s: %d", query, rec.Code)
		}
	}
}

func TestQualityResponseAndSourceSelection(t *testing.T) {
	src := &fakeQuality{q: Quality{Source: "ieee-cis", RunID: "a/b", FraudValueFlaggedMinor: "18446744073709551614", FraudValueAllowedMinor: "0"}}
	rec := qualityGet(src, "/stats/quality?source=ieee-cis&run_id=a%2Fb", "quality-key")
	if rec.Code != 200 || src.source != "ieee-cis" || src.run != "a/b" {
		t.Fatal(rec.Code, src)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body["precision"]) != "null" || string(body["fraud_value_flagged_minor"]) != `"18446744073709551614"` {
		t.Fatal(rec.Body.String())
	}
	for _, private := range []string{"transaction_id", "is_fraud", "scenario", "request_json"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("private answer in report", private)
		}
	}
	rec = qualityGet(src, "/stats/quality?run_id=r", "quality-key")
	if rec.Code != 200 || src.source != "simulator" {
		t.Fatal(src.source, rec.Code)
	}
	rec = qualityGet(src, "/stats/quality/runs?source=ieee-cis&after=a%2Fb&limit=7", "quality-key")
	if rec.Code != 200 || src.after != "a/b" || src.limit != 7 || src.source != "ieee-cis" {
		t.Fatal(src, rec.Code)
	}
}

func TestQualityFailuresAreIsolatedAndSanitized(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{ErrQualityRunNotFound, 404}, {ErrQualityUnavailable, 503}, {errors.New("password=secret"), 503}} {
		rec := qualityGet(&fakeQuality{err: tc.err}, "/stats/quality?run_id=r", "quality-key")
		if rec.Code != tc.status || strings.Contains(rec.Body.String(), "secret") {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	rec := qualityGet(NewQualityStore(nil), "/stats/quality/runs", "quality-key")
	if rec.Code != 503 {
		t.Fatal(rec.Code)
	}
}

func TestQualityRatesWithMissingClasses(t *testing.T) {
	for _, tc := range []struct {
		q                      Quality
		precision, recall, fpr *float64
	}{
		{Quality{}, nil, nil, nil},
		{Quality{TrueNegatives: 2}, nil, nil, ptr(0.0)},
		{Quality{FalseNegatives: 1}, nil, ptr(0.0), nil},
		{Quality{TruePositives: 1}, ptr(1.0), ptr(1.0), nil},
		{Quality{FalsePositives: 1}, ptr(0.0), nil, ptr(1.0)},
	} {
		tc.q.calculateRates()
		for _, p := range [][2]*float64{{tc.q.Precision, tc.precision}, {tc.q.Recall, tc.recall}, {tc.q.FalsePositiveRate, tc.fpr}} {
			if (p[0] == nil) != (p[1] == nil) || p[0] != nil && *p[0] != *p[1] {
				t.Fatal(tc.q)
			}
		}
	}
}

type qualitySessions struct{}

func (qualitySessions) Identify(_ context.Context, token string) (platform.Identity, error) {
	if token != "quality-key" {
		return platform.Identity{}, platform.ErrNoSession
	}
	return platform.Identity{Username: "quality-admin", Role: platform.RoleAdmin}, nil
}
