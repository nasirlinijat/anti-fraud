package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

type simulationAnswerStub struct {
	queries []labels.SimulationAnswerQuery
}

func (s *simulationAnswerStub) Page(_ context.Context, q labels.SimulationAnswerQuery) (labels.SimulationAnswerPage, error) {
	s.queries = append(s.queries, q)
	return labels.SimulationAnswerPage{Items: []labels.SimulationAnswer{}}, nil
}

func TestSimulationAnswersRequireAdminSession(t *testing.T) {
	reader := &simulationAnswerStub{}
	mux := http.NewServeMux()
	labels.RegisterSimulationAnswerRoutes(mux, reader, true, platform.Access{Sessions: consoleTestSessions})
	for _, tc := range []struct {
		name, header, credential string
		status                   int
	}{
		{"reviewer", "Authorization", "Bearer mock-reviewer-token", http.StatusForbidden},
		{"service key", "X-API-Key", "mock-service-key", http.StatusUnauthorized},
		{"admin", "Authorization", "Bearer mock-admin-token", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/simulations/sim-first/answers?filter=exceptions&limit=2&before=9", nil)
			r.Header.Set(tc.header, tc.credential)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status = %d, cache policy = %q, body = %s", w.Code, w.Header().Get("Cache-Control"), w.Body)
			}
		})
	}
	want := labels.SimulationAnswerQuery{RunID: "sim-first", Before: 9, Limit: 2, Exceptions: true}
	if len(reader.queries) != 1 || reader.queries[0] != want {
		t.Fatalf("reader calls = %+v, want only %+v", reader.queries, want)
	}
}

func TestSimulationAnswersRejectInvalidPagination(t *testing.T) {
	reader := &simulationAnswerStub{}
	mux := http.NewServeMux()
	labels.RegisterSimulationAnswerRoutes(mux, reader, true, platform.Access{Sessions: consoleTestSessions})
	for _, query := range []string{"limit=0", "limit=51", "limit=1&limit=2", "before=0", "before=bad", "filter=all", "source=ieee-cis", "limit=%xx"} {
		req := httptest.NewRequest(http.MethodGet, "/simulations/sim-first/answers?"+query, nil)
		req.Header.Set("Authorization", "Bearer mock-admin-token")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("query %q returned %d, want 400", query, w.Code)
		}
	}
	if len(reader.queries) > 0 {
		t.Fatalf("invalid queries reached the reader: %+v", reader.queries)
	}
}
