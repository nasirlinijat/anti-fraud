package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeSessions map[string]Identity

func (f fakeSessions) Identify(_ context.Context, token string) (Identity, error) {
	if token == "broken-token" {
		return Identity{}, errors.New("database is down")
	}
	identity, ok := f[token]
	if !ok {
		return Identity{}, ErrNoSession
	}
	return identity, nil
}

var testSessions = fakeSessions{
	"admin-token":    {Username: "ada", Role: RoleAdmin},
	"reviewer-token": {Username: "rita", Role: RoleReviewer},
}

// accessCase is one request to an access-checked endpoint. wantActor is the
// ID passed on to the endpoint, empty when the request is rejected or the
// endpoint takes no ID.
type accessCase struct {
	name          string
	apiKey        string
	authorization string
	want          int
	wantActor     string
}

func testAccess(t *testing.T) Access {
	t.Helper()
	return Access{Sessions: testSessions}
}

// serveAccessCase sends the case's request to handler and checks the status,
// the actor passed on, and the session challenge on a 401.
func serveAccessCase(t *testing.T, handler http.Handler, actor *string, tc accessCase) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if tc.apiKey != "" {
		req.Header.Set(APIKeyHeader, tc.apiKey)
	}
	if tc.authorization != "" {
		req.Header.Set("Authorization", tc.authorization)
	}
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != tc.want {
		t.Fatalf("status = %d, want %d, body %s", rec.Code, tc.want, rec.Body)
	}
	if *actor != tc.wantActor {
		t.Fatalf("actor = %q, want %q", *actor, tc.wantActor)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	challenge := rec.Header().Get("WWW-Authenticate")
	if rec.Code == http.StatusUnauthorized && challenge != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", challenge)
	}
}

func TestConfigReadRequiresAdminSession(t *testing.T) {
	for _, tc := range []accessCase{
		{name: "service key", apiKey: "service-key", want: http.StatusUnauthorized},
		{name: "admin session", authorization: "Bearer admin-token", want: http.StatusNoContent},
		{name: "reviewer session", authorization: "Bearer reviewer-token", want: http.StatusForbidden},
		{name: "unknown session", authorization: "Bearer unknown-token", want: http.StatusUnauthorized},
		{name: "not a bearer token", authorization: "Basic YWRhOnNlY3JldA==", want: http.StatusUnauthorized},
		{name: "session check fails", authorization: "Bearer broken-token", want: http.StatusInternalServerError},
		{name: "invalid session with valid key", apiKey: "service-key", authorization: "Bearer unknown-token", want: http.StatusUnauthorized},
		{name: "operator key", apiKey: "admin-token", want: http.StatusUnauthorized},
		{name: "nothing", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actor string
			handler := testAccess(t).ConfigRead(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))

			serveAccessCase(t, handler, &actor, tc)
		})
	}
}

func TestConfigWriteRequiresAdminSession(t *testing.T) {
	for _, tc := range []accessCase{
		{name: "operator key", apiKey: "admin-token", want: http.StatusUnauthorized},
		{name: "admin session", authorization: "Bearer admin-token", want: http.StatusNoContent, wantActor: "ada"},
		{name: "reviewer session", authorization: "Bearer reviewer-token", want: http.StatusForbidden},
		{name: "unknown session", authorization: "Bearer unknown-token", want: http.StatusUnauthorized},
		{name: "service key", apiKey: "service-key", want: http.StatusUnauthorized},
		{name: "legacy analyst key", apiKey: "analyst-key", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actor string
			handler := testAccess(t).ConfigWrite(func(w http.ResponseWriter, _ *http.Request, id string) {
				actor = id
				w.WriteHeader(http.StatusNoContent)
			})

			serveAccessCase(t, handler, &actor, tc)
		})
	}
}

func TestReviewRequiresReviewerOrAdminSession(t *testing.T) {
	for _, tc := range []accessCase{
		{name: "legacy analyst key", apiKey: "analyst-key", want: http.StatusUnauthorized},
		{name: "reviewer session", authorization: "Bearer reviewer-token", want: http.StatusNoContent, wantActor: "rita"},
		{name: "admin session", authorization: "Bearer admin-token", want: http.StatusNoContent, wantActor: "ada"},
		{name: "unknown session", authorization: "Bearer unknown-token", want: http.StatusUnauthorized},
		{name: "service key", apiKey: "service-key", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actor string
			handler := testAccess(t).Review(func(w http.ResponseWriter, _ *http.Request, a Actor) {
				actor = a.ID
				w.WriteHeader(http.StatusNoContent)
			})

			serveAccessCase(t, handler, &actor, tc)
		})
	}
}

func TestSessionsWorkWithoutConfiguredKeys(t *testing.T) {
	for _, tc := range []struct {
		accessCase
		handler func(Access, *string) http.Handler
	}{
		{accessCase{name: "review by key", apiKey: "analyst-key", want: http.StatusUnauthorized}, reviewHandler},
		{accessCase{name: "review by session", authorization: "Bearer reviewer-token", want: http.StatusNoContent, wantActor: "rita"}, reviewHandler},
		{accessCase{name: "write by key", apiKey: "admin-token", want: http.StatusUnauthorized}, configWriteHandler},
		{accessCase{name: "write by session", authorization: "Bearer admin-token", want: http.StatusNoContent, wantActor: "ada"}, configWriteHandler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actor string
			access := Access{Sessions: testSessions}

			serveAccessCase(t, tc.handler(access, &actor), &actor, tc.accessCase)
		})
	}
}

func TestSessionWithoutSessionStoreIsRejected(t *testing.T) {
	var actor string
	access := testAccess(t)
	access.Sessions = nil
	handler := access.ConfigRead(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	serveAccessCase(t, handler, &actor, accessCase{authorization: "Bearer admin-token", want: http.StatusUnauthorized})
}

func reviewHandler(access Access, actor *string) http.Handler {
	return access.Review(func(w http.ResponseWriter, _ *http.Request, a Actor) {
		*actor = a.ID
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestReviewDerivesTheActorOnlyFromASession(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		value  string
		want   Actor
	}{
		{"legacy analyst key", APIKeyHeader, "analyst-key", Actor{}},
		{"reviewer session", "Authorization", "Bearer reviewer-token", Actor{ID: "rita"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got Actor
			handler := testAccess(t).Review(func(w http.ResponseWriter, _ *http.Request, a Actor) {
				got = a
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(tc.header, tc.value)

			handler.ServeHTTP(httptest.NewRecorder(), req)

			if got != tc.want {
				t.Fatalf("actor = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func configWriteHandler(access Access, actor *string) http.Handler {
	return access.ConfigWrite(func(w http.ResponseWriter, _ *http.Request, id string) {
		*actor = id
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestAdminAcceptsOnlyAnAdminSession(t *testing.T) {
	for _, tc := range []accessCase{
		{name: "admin session", authorization: "Bearer admin-token", want: http.StatusNoContent, wantActor: "ada"},
		{name: "reviewer session", authorization: "Bearer reviewer-token", want: http.StatusForbidden},
		{name: "service key", apiKey: "service-key", want: http.StatusUnauthorized},
		{name: "operator key", apiKey: "admin-token", want: http.StatusUnauthorized},
		{name: "legacy analyst key", apiKey: "analyst-key", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actor string
			handler := testAccess(t).Admin(func(w http.ResponseWriter, _ *http.Request, username string) {
				actor = username
				w.WriteHeader(http.StatusNoContent)
			})

			serveAccessCase(t, handler, &actor, tc)
		})
	}
}
