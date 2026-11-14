package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

// reviewerWork returns the entry of the username in the assignments response,
// failing the test when it is missing.
func reviewerWork(t *testing.T, body []byte, username string) reviewerWorkResponse {
	t.Helper()
	var got assignmentsResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode assignments: %v", err)
	}
	for _, reviewer := range got.Reviewers {
		if reviewer.Username == username {
			return reviewer
		}
	}
	t.Fatalf("%s is missing from %s", username, body)
	return reviewerWorkResponse{}
}

func TestAssignmentsShowEachReviewersOpenClaimsAndDecisions(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	admin := server.signIn(t, "assign-test-admin", models.RoleAdmin)
	rita := server.signIn(t, "assign-test-rita", models.RoleReviewer)
	server.signIn(t, "assign-test-sam", models.RoleReviewer)
	open := insertOpenReview(t, tx, "assign-open")
	decided := insertOpenReview(t, tx, "assign-decided")
	for _, id := range []string{open, decided} {
		if rec := server.send(http.MethodPost, "/reviews/"+id+"/claim", rita, ""); rec.Code != http.StatusCreated {
			t.Fatalf("claim %s = %d %s", id, rec.Code, rec.Body)
		}
	}
	if rec := server.send(http.MethodPost, "/reviews/"+decided+"/decision", rita, `{"decision":"approve"}`); rec.Code != http.StatusCreated {
		t.Fatalf("decision = %d %s", rec.Code, rec.Body)
	}

	rec := server.send(http.MethodGet, "/admin/assignments", admin, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("assignments = %d %s", rec.Code, rec.Body)
	}
	ritas := reviewerWork(t, rec.Body.Bytes(), "assign-test-rita")
	if ritas.Decided != 1 || len(ritas.OpenClaims) != 1 || ritas.OpenClaims[0].PurchaseID != open {
		t.Fatalf("rita = %+v, want 1 decided and open claim %s", ritas, open)
	}
	claim := ritas.OpenClaims[0]
	if claim.AmountMinor != "2500" || claim.RuleScore != 60 || claim.ModelScore != nil || claim.ClaimedAt.IsZero() {
		t.Fatalf("open claim = %+v", claim)
	}
	if sams := reviewerWork(t, rec.Body.Bytes(), "assign-test-sam"); sams.Decided != 0 || len(sams.OpenClaims) != 0 {
		t.Fatalf("sam = %+v, want no work", sams)
	}
}

func TestDisabledReviewerWithOpenClaimsStaysInAssignments(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	admin := server.signIn(t, "assign-test-admin", models.RoleAdmin)
	rita := server.signIn(t, "assign-test-rita", models.RoleReviewer)
	open := insertOpenReview(t, tx, "assign-disabled")
	if rec := server.send(http.MethodPost, "/reviews/"+open+"/claim", rita, ""); rec.Code != http.StatusCreated {
		t.Fatalf("claim = %d %s", rec.Code, rec.Body)
	}
	if _, err := tx.Exec(context.Background(), `UPDATE accounts SET disabled_at = now() WHERE username = 'assign-test-rita'`); err != nil {
		t.Fatal(err)
	}

	rec := server.send(http.MethodGet, "/admin/assignments", admin, "")

	ritas := reviewerWork(t, rec.Body.Bytes(), "assign-test-rita")
	if !ritas.Disabled || len(ritas.OpenClaims) != 1 {
		t.Fatalf("rita = %+v, want disabled with one open claim", ritas)
	}
}

func TestAdminReleasesAClaimBackToTheQueue(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	admin := server.signIn(t, "assign-test-admin", models.RoleAdmin)
	rita := server.signIn(t, "assign-test-rita", models.RoleReviewer)
	sam := server.signIn(t, "assign-test-sam", models.RoleReviewer)
	open := insertOpenReview(t, tx, "release-open")
	decided := insertOpenReview(t, tx, "release-decided")
	for _, id := range []string{open, decided} {
		if rec := server.send(http.MethodPost, "/reviews/"+id+"/claim", rita, ""); rec.Code != http.StatusCreated {
			t.Fatalf("claim %s = %d %s", id, rec.Code, rec.Body)
		}
	}
	if rec := server.send(http.MethodPost, "/reviews/"+decided+"/decision", rita, `{"decision":"reject"}`); rec.Code != http.StatusCreated {
		t.Fatalf("decision = %d %s", rec.Code, rec.Body)
	}

	byReviewer := server.send(http.MethodDelete, "/reviews/"+open+"/claim", sam, "")
	released := server.send(http.MethodDelete, "/reviews/"+open+"/claim", admin, "")
	again := server.send(http.MethodDelete, "/reviews/"+open+"/claim", admin, "")
	ofDecided := server.send(http.MethodDelete, "/reviews/"+decided+"/claim", admin, "")
	samClaims := server.send(http.MethodPost, "/reviews/"+open+"/claim", sam, "")

	if byReviewer.Code != http.StatusForbidden {
		t.Fatalf("reviewer release = %d, want 403", byReviewer.Code)
	}
	if released.Code != http.StatusNoContent || again.Code != http.StatusNotFound || ofDecided.Code != http.StatusConflict {
		t.Fatalf("release = %d, again = %d, decided = %d; want 204, 404, 409", released.Code, again.Code, ofDecided.Code)
	}
	if samClaims.Code != http.StatusCreated {
		t.Fatalf("claim after release = %d %s, want 201", samClaims.Code, samClaims.Body)
	}
}

func TestReviewersCannotSeeAssignments(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	rita := server.signIn(t, "assign-test-rita", models.RoleReviewer)

	bySession := server.send(http.MethodGet, "/admin/assignments", rita, "")
	byKey := reviewRequest(server.mux, http.MethodGet, "/admin/assignments", queueTestKey, "")

	if bySession.Code != http.StatusForbidden || byKey.Code != http.StatusUnauthorized {
		t.Fatalf("reviewer = %d, invalid session = %d; want 403 and 401", bySession.Code, byKey.Code)
	}
}
