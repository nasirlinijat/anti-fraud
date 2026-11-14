//go:build integration

package integration

import (
	"net/http"
	"testing"
)

func TestFixtureRequestsReuseSeparateRoleSessions(t *testing.T) {
	a := newApp(t)
	for range 2 {
		expect[map[string]any](t, a.do(t, http.MethodGet, "/rulesets/active", adminToken, ""), http.StatusOK)
		expect[map[string]any](t, a.do(t, http.MethodGet, "/rulesets/active", reviewerToken, ""), http.StatusForbidden)
	}
	var count int
	if err := a.tx.QueryRow(t.Context(), "SELECT count(*) FROM sessions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("sessions = %d, want one per role", count)
	}
}

func TestFixtureDoesNotRefreshRevokedSessions(t *testing.T) {
	for _, reason := range []string{"logout", "disabled account"} {
		t.Run(reason, func(t *testing.T) {
			a := newApp(t)
			expect[map[string]any](t, a.do(t, http.MethodGet, "/rulesets/active", adminToken, ""), http.StatusOK)
			session := a.fixtureSession(t, adminToken)
			if reason == "logout" {
				if err := a.sessions.Logout(t.Context(), session.Token); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := a.tx.Exec(t.Context(), "UPDATE accounts SET disabled_at=now() WHERE id=$1", session.Account.ID); err != nil {
					t.Fatal(err)
				}
			}
			expect[map[string]any](t, a.do(t, http.MethodGet, "/rulesets/active", adminToken, ""), http.StatusUnauthorized)
		})
	}
}

func TestFixtureSessionsDoNotSurviveRollback(t *testing.T) {
	var previous string
	for range 2 {
		t.Run("transaction", func(t *testing.T) {
			a := newApp(t)
			session := a.fixtureSession(t, adminToken)
			if previous != "" {
				expect[map[string]any](t, a.doAs(t, http.MethodGet, "/rulesets/active", previous, ""), http.StatusUnauthorized)
			}
			previous = session.Token
			expect[map[string]any](t, a.do(t, http.MethodGet, "/rulesets/active", adminToken, ""), http.StatusOK)
		})
	}
}
