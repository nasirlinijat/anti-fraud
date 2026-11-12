package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/account"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/auth"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

// sessionTestServer serves the review and blocklist routes with sessions as
// the only credentials, and returns a session token for each role.
func sessionTestServer(t *testing.T, tx pgx.Tx) (mux *http.ServeMux, adminToken, reviewerToken string) {
	t.Helper()
	accounts := account.NewService(storage.NewAccountStore(tx))
	sessions := auth.NewService(storage.NewAccountStore(tx), storage.NewSessionStore(tx))
	signIn := func(username string, role models.Role) string {
		t.Helper()
		const password = "session test password"
		if _, err := accounts.Create(context.Background(), account.NewAccount{Username: username, Password: password, Role: role}); err != nil {
			t.Fatalf("create %s: %v", username, err)
		}
		session, err := sessions.Login(context.Background(), username, password)
		if err != nil {
			t.Fatalf("sign in %s: %v", username, err)
		}
		return session.Token
	}
	access := platform.Access{Sessions: sessions}
	mux = http.NewServeMux()
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(storage.NewReviewStore(tx)), false), access)
	RegisterBlocklistRoutes(mux, NewBlocklistHandler(blocklist.NewService(storage.NewBlocklistStore(tx))), access)
	return mux, signIn("session-test-admin", models.RoleAdmin), signIn("session-test-reviewer", models.RoleReviewer)
}

func sendWithSession(mux *http.ServeMux, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestReviewerSessionDecisionSavesTheUsername(t *testing.T) {
	tx := testTx(t)
	mux, _, reviewerToken := sessionTestServer(t, tx)
	id := insertReviewDecision(t, tx, "session-decision", "review", time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), nil, `[]`)

	rec := sendWithSession(mux, http.MethodPost, "/reviews/"+strconv.FormatInt(id, 10)+"/decision", reviewerToken, `{"decision":"approve"}`)

	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"analyst_id":"session-test-reviewer"`) {
		t.Fatalf("decision = %d %s", rec.Code, rec.Body)
	}
	var analyst string
	if err := tx.QueryRow(context.Background(), `SELECT analyst_id FROM review_resolutions WHERE purchase_id = $1`, id).Scan(&analyst); err != nil {
		t.Fatal(err)
	}
	if analyst != "session-test-reviewer" {
		t.Fatalf("saved analyst = %q, want session-test-reviewer", analyst)
	}
}

func TestAdminSessionBlocklistChangeAuditsTheUsername(t *testing.T) {
	tx := testTx(t)
	mux, adminToken, reviewerToken := sessionTestServer(t, tx)
	body := `{"type":"device","value":"session-audit-device","reason":"session test"}`

	if rec := sendWithSession(mux, http.MethodPost, "/blocklist", reviewerToken, body); rec.Code != http.StatusForbidden {
		t.Fatalf("reviewer add = %d %s, want 403", rec.Code, rec.Body)
	}
	rec := sendWithSession(mux, http.MethodPost, "/blocklist", adminToken, body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("admin add = %d %s", rec.Code, rec.Body)
	}
	var actors []string
	if err := tx.QueryRow(context.Background(), `
		SELECT array_agg(actor_id) FROM configuration_audit_events
		WHERE action = 'blocklist.add' AND details->>'value' = 'session-audit-device'`).Scan(&actors); err != nil {
		t.Fatal(err)
	}
	if len(actors) != 1 || actors[0] != "session-test-admin" {
		t.Fatalf("audited actors = %v, want [session-test-admin]", actors)
	}
}
