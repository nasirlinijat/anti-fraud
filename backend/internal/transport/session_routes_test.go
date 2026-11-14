package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	quality "github.com/nasirlinijat/anti-fraud/backend/internal/stats"
)

func TestConsoleRoutesRejectKeysBeforeCallingServices(t *testing.T) {
	mux := http.NewServeMux()
	access := platform.Access{Sessions: consoleTestSessions}
	RegisterReviewRoutes(mux, NewReviewHandler(nil, true), access)
	RegisterAccountRoutes(mux, NewAccountHandler(nil), access)
	RegisterBlocklistRoutes(mux, NewBlocklistHandler(nil), access)
	RegisterRulesetRoutes(mux, NewRulesetHandler(nil), access)
	RegisterStatsRoutes(mux, NewStatsHandler(nil), access)
	RegisterModelRoutes(mux, nil, nil, access)
	RegisterSimulationRoutes(mux, NewSimulationHandler(nil), true, access)
	quality.RegisterQualityRoutes(mux, nil, access)
	routes := []string{
		"GET /reviews", "GET /reviews/1", "GET /reviews/1/resolved", "GET /reviews/1/network",
		"GET /reviews/1/network/purchases", "GET /reviews/1/network/reviews",
		"POST /reviews/1/claim", "DELETE /reviews/1/claim", "POST /reviews/1/decision",
		"POST /reviews/claims", "POST /reviews/decisions", "GET /admin/assignments",
		"GET /admin/accounts", "POST /admin/accounts", "POST /admin/accounts/someone/disable",
		"GET /model", "GET /stats", "GET /stats/pipeline/recent", "GET /stats/quality", "GET /stats/quality/runs",
		"GET /rulesets", "GET /rulesets/active", "GET /rulesets/1", "POST /rulesets",
		"GET /blocklist", "POST /blocklist", "DELETE /blocklist/1",
		"GET /simulations", "POST /simulations", "GET /simulations/one", "POST /simulations/one/stop", "POST /simulations/reset",
	}
	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			for _, key := range []string{"", mockServiceKey, mockAdminToken, mockReviewerToken} {
				req := httptest.NewRequest(method, path, nil)
				req.Header.Set("X-API-Key", key)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != "Bearer" || rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("key %q: %d %v", key, rec.Code, rec.Header())
				}
			}
		})
	}
}
