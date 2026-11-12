package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/account"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/auth"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

func consoleWithoutDatabase(t *testing.T) *http.ServeMux {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sessions := auth.NewService(storage.NewAccountStore(pool), storage.NewSessionStore(pool))
	access := platform.Access{ServiceKey: serviceTestKey, Analysts: testAnalystKeys(t), Sessions: sessions}
	mux := http.NewServeMux()
	RegisterAuthRoutes(mux, NewAuthHandler(sessions))
	RegisterAccountRoutes(mux, NewAccountHandler(account.NewService(storage.NewAccountStore(pool))), access)
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(storage.NewReviewStore(pool)), false), access)
	RegisterRulesetRoutes(mux, NewRulesetHandler(ruleset.NewService(storage.NewRulesetStore(pool))), access)
	RegisterBlocklistRoutes(mux, NewBlocklistHandler(blocklist.NewService(storage.NewBlocklistStore(pool))), access)
	RegisterStatsRoutes(mux, NewStatsHandler(stats.NewService(storage.NewStatsStore(pool))), access)
	return mux
}

func TestConsoleAnswers503WhenTheDatabaseIsUnreachable(t *testing.T) {
	mux := consoleWithoutDatabase(t)
	tests := []struct {
		name, method, path, auth, body string
	}{
		{"sign-in check of a console session", http.MethodGet, "/admin/accounts", "Bearer some-session-token", ""},
		{"signing in", http.MethodPost, "/auth/login", "", `{"username":"analyst","password":"a long enough password"}`},
		{"current account", http.MethodGet, "/auth/me", "Bearer some-session-token", ""},
		{"review queue with an analyst key", http.MethodGet, "/reviews", "", ""},
		{"review case with an analyst key", http.MethodGet, "/reviews/7", "", ""},
		{"rulesets with the service key", http.MethodGet, "/rulesets", "", ""},
		{"active ruleset with the service key", http.MethodGet, "/rulesets/active", "", ""},
		{"blocklist with the service key", http.MethodGet, "/blocklist", "", ""},
		{"stats with the service key", http.MethodGet, "/stats", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			switch {
			case tt.auth != "":
				req.Header.Set("Authorization", tt.auth)
			case strings.HasPrefix(tt.path, "/reviews"):
				req.Header.Set("X-API-Key", queueTestKey)
			case !strings.HasPrefix(tt.path, "/auth"):
				req.Header.Set("X-API-Key", serviceTestKey)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" || !strings.Contains(rec.Body.String(), platform.DatabaseUnavailableMessage) {
				t.Fatalf("status %d, Retry-After %q, body %s; want 503 with Retry-After 1", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
			}
		})
	}
}

func TestOtherServerErrorsStay500(t *testing.T) {
	service := mocks.NewMockRulesetService(gomock.NewController(t))
	service.EXPECT().List(gomock.Any()).Return(nil, errors.New("decode ruleset: unexpected end of JSON"))
	mux := http.NewServeMux()
	RegisterRulesetRoutes(mux, NewRulesetHandler(service), platform.Access{ServiceKey: mockServiceKey})

	rec := send(mux, http.MethodGet, "/rulesets", mockServiceKey, "")
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Retry-After") != "" || !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("status %d, Retry-After %q, body %s; want a plain 500", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}
