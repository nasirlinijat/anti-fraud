package transport

import (
	"context"
	"encoding/json"
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

// sessionTestServer serves the review, blocklist, and account routes against
// the test transaction, to sessions and to the test analyst keys.
type sessionTestServer struct {
	mux      *http.ServeMux
	accounts *account.Service
	sessions *auth.Service
}

func newSessionTestServer(t *testing.T, tx pgx.Tx) *sessionTestServer {
	t.Helper()
	sessions := auth.NewService(storage.NewAccountStore(tx), storage.NewSessionStore(tx))
	access := platform.Access{Analysts: testAnalystKeys(t), Sessions: sessions}
	mux := http.NewServeMux()
	RegisterReviewRoutes(mux, NewReviewHandler(review.NewService(storage.NewReviewStore(tx)), false), access)
	RegisterBlocklistRoutes(mux, NewBlocklistHandler(blocklist.NewService(storage.NewBlocklistStore(tx))), access)
	accounts := account.NewService(storage.NewAccountStore(tx))
	RegisterAccountRoutes(mux, NewAccountHandler(accounts), access)
	return &sessionTestServer{mux: mux, accounts: accounts, sessions: sessions}
}

// signIn creates an account and returns a session token for it.
func (s *sessionTestServer) signIn(t *testing.T, username string, role models.Role) string {
	t.Helper()
	const password = "session test password"
	if _, err := s.accounts.Create(context.Background(), account.NewAccount{Username: username, Password: password, Role: role}); err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	session, err := s.sessions.Login(context.Background(), username, password)
	if err != nil {
		t.Fatalf("sign in %s: %v", username, err)
	}
	return session.Token
}

func (s *sessionTestServer) send(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func insertOpenReview(t *testing.T, tx pgx.Tx, transactionID string) string {
	t.Helper()
	id := insertReviewDecision(t, tx, transactionID, "review", time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), nil, `[]`)
	return strconv.FormatInt(id, 10)
}

func savedAnalyst(t *testing.T, tx pgx.Tx, purchaseID string) string {
	t.Helper()
	var analyst string
	err := tx.QueryRow(context.Background(), `SELECT COALESCE((SELECT analyst_id FROM review_resolutions WHERE purchase_id = $1::bigint), '')`, purchaseID).Scan(&analyst)
	if err != nil {
		t.Fatal(err)
	}
	return analyst
}

func TestReviewerSessionDecisionSavesTheUsername(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	rita := server.signIn(t, "session-test-rita", models.RoleReviewer)
	id := insertOpenReview(t, tx, "session-decision")
	if rec := server.send(http.MethodPost, "/reviews/"+id+"/claim", rita, ""); rec.Code != http.StatusCreated {
		t.Fatalf("claim = %d %s", rec.Code, rec.Body)
	}

	rec := server.send(http.MethodPost, "/reviews/"+id+"/decision", rita, `{"decision":"approve"}`)

	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"analyst_id":"session-test-rita"`) {
		t.Fatalf("decision = %d %s", rec.Code, rec.Body)
	}
	if got := savedAnalyst(t, tx, id); got != "session-test-rita" {
		t.Fatalf("saved analyst = %q, want session-test-rita", got)
	}
}

func TestSessionMustClaimBeforeDeciding(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	rita := server.signIn(t, "session-test-rita", models.RoleReviewer)
	id := insertOpenReview(t, tx, "session-unclaimed")

	rec := server.send(http.MethodPost, "/reviews/"+id+"/decision", rita, `{"decision":"reject"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("decision = %d %s, want 409", rec.Code, rec.Body)
	}
	if got := savedAnalyst(t, tx, id); got != "" {
		t.Fatalf("a decision was saved by %q", got)
	}
}

func TestClaimedPurchaseBelongsToOneAccount(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	rita := server.signIn(t, "session-test-rita", models.RoleReviewer)
	sam := server.signIn(t, "session-test-sam", models.RoleReviewer)
	id := insertOpenReview(t, tx, "session-contested")
	first := server.send(http.MethodPost, "/reviews/"+id+"/claim", rita, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first claim = %d %s", first.Code, first.Body)
	}

	again := server.send(http.MethodPost, "/reviews/"+id+"/claim", rita, "")
	other := server.send(http.MethodPost, "/reviews/"+id+"/claim", sam, "")
	otherDecision := server.send(http.MethodPost, "/reviews/"+id+"/decision", sam, `{"decision":"approve"}`)

	if again.Code != http.StatusOK || again.Body.String() != first.Body.String() {
		t.Fatalf("repeated claim = %d %s, want 200 %s", again.Code, again.Body, first.Body)
	}
	if other.Code != http.StatusConflict || otherDecision.Code != http.StatusConflict {
		t.Fatalf("other account: claim %d, decision %d, want 409 for both", other.Code, otherDecision.Code)
	}
	var claim claimResponse
	if err := json.Unmarshal(first.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.ClaimedBy != "session-test-rita" || claim.ClaimedAt.Location() != time.UTC {
		t.Fatalf("claim = %+v, want rita in UTC", claim)
	}
}

func TestQueueShowsClaimsAndFiltersOwnClaims(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	rita := server.signIn(t, "session-test-rita", models.RoleReviewer)
	sam := server.signIn(t, "session-test-sam", models.RoleReviewer)
	claimed := insertOpenReview(t, tx, "session-claimed")
	unclaimed := insertOpenReview(t, tx, "session-unclaimed")
	if rec := server.send(http.MethodPost, "/reviews/"+claimed+"/claim", rita, ""); rec.Code != http.StatusCreated {
		t.Fatalf("claim = %d %s", rec.Code, rec.Body)
	}

	ritas := readQueuePage(t, server.send(http.MethodGet, "/reviews?claimed=mine", rita, ""))
	sams := readQueuePage(t, server.send(http.MethodGet, "/reviews?claimed=mine", sam, ""))

	if len(ritas.Items) != 1 || ritas.Items[0].PurchaseID != claimed || ritas.Items[0].ClaimedBy == nil || *ritas.Items[0].ClaimedBy != "session-test-rita" {
		t.Fatalf("rita's claims = %+v, want only %s", ritas.Items, claimed)
	}
	if len(sams.Items) != 0 {
		t.Fatalf("sam's claims = %+v, want none", sams.Items)
	}
	var open caseResponse
	if err := json.Unmarshal(server.send(http.MethodGet, "/reviews/"+unclaimed, sam, "").Body.Bytes(), &open); err != nil {
		t.Fatal(err)
	}
	if open.ClaimedBy != nil || open.ClaimedAt != nil {
		t.Fatalf("unclaimed case shows claim %v at %v", open.ClaimedBy, open.ClaimedAt)
	}
}

func TestAnalystKeysCannotClaimButStillDecide(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	id := insertOpenReview(t, tx, "session-key-analyst")

	claim := reviewRequest(server.mux, http.MethodPost, "/reviews/"+id+"/claim", queueTestKey, "")
	mine := reviewRequest(server.mux, http.MethodGet, "/reviews?claimed=mine", queueTestKey, "")
	decision := reviewRequest(server.mux, http.MethodPost, "/reviews/"+id+"/decision", queueTestKey, `{"decision":"approve"}`)

	if claim.Code != http.StatusForbidden || mine.Code != http.StatusBadRequest {
		t.Fatalf("claim = %d, claimed=mine = %d, want 403 and 400", claim.Code, mine.Code)
	}
	if decision.Code != http.StatusCreated || savedAnalyst(t, tx, id) != "first" {
		t.Fatalf("key decision = %d %s, want saved by first", decision.Code, decision.Body)
	}
}

func TestAdminSessionBlocklistChangeAuditsTheUsername(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	admin := server.signIn(t, "session-test-admin", models.RoleAdmin)
	reviewer := server.signIn(t, "session-test-rita", models.RoleReviewer)
	body := `{"type":"device","value":"session-audit-device","reason":"session test"}`

	if rec := server.send(http.MethodPost, "/blocklist", reviewer, body); rec.Code != http.StatusForbidden {
		t.Fatalf("reviewer add = %d %s, want 403", rec.Code, rec.Body)
	}
	rec := server.send(http.MethodPost, "/blocklist", admin, body)

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
