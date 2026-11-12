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
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/auth"
)

type fakeAuthService struct {
	loginErr  error
	sessions  map[string]models.Account
	loggedOut []string
}

func (f *fakeAuthService) Login(_ context.Context, username, password string) (models.Session, error) {
	if f.loginErr != nil {
		return models.Session{}, f.loginErr
	}
	return models.Session{
		Token:     "token-for-" + username,
		ExpiresAt: time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC),
		Account:   models.Account{ID: 1, Username: username, Role: models.RoleAdmin},
	}, nil
}

func (f *fakeAuthService) Authenticate(_ context.Context, token string) (models.Account, error) {
	account, ok := f.sessions[token]
	if !ok {
		return models.Account{}, models.ErrSessionNotFound
	}
	return account, nil
}

func (f *fakeAuthService) Logout(_ context.Context, token string) error {
	f.loggedOut = append(f.loggedOut, token)
	return nil
}

func newAuthTestServer(service *fakeAuthService) *http.ServeMux {
	mux := http.NewServeMux()
	RegisterAuthRoutes(mux, NewAuthHandler(service))
	return mux
}

func serveAuth(mux *http.ServeMux, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestLoginReturnsTheSession(t *testing.T) {
	mux := newAuthTestServer(&fakeAuthService{})

	rec := serveAuth(mux, http.MethodPost, "/auth/login", `{"username":"alice","password":"correct horse battery"}`, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	want := `{"token":"token-for-alice","expires_at":"2026-11-12T20:00:00Z","account":{"username":"alice","role":"admin"}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestLoginRejectsInvalidBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"not JSON", `username=alice`, http.StatusBadRequest},
		{"unknown field", `{"username":"alice","password":"secret password","remember":true}`, http.StatusBadRequest},
		{"two objects", `{"username":"alice","password":"secret password"}{}`, http.StatusBadRequest},
		{"missing password", `{"username":"alice"}`, http.StatusBadRequest},
		{"missing username", `{"password":"secret password"}`, http.StatusBadRequest},
		{"too large", `{"username":"alice","password":"` + strings.Repeat("a", 2000) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := newAuthTestServer(&fakeAuthService{})

			rec := serveAuth(mux, http.MethodPost, "/auth/login", tc.body, nil)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d, body %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestLoginMapsServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		want       int
		retryAfter string
	}{
		{"invalid credentials", auth.ErrInvalidCredentials, http.StatusUnauthorized, ""},
		{"throttled", &auth.ThrottledError{RetryAfter: 299*time.Second + 200*time.Millisecond}, http.StatusTooManyRequests, "300"},
		{"database down", errors.New("connection refused"), http.StatusInternalServerError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := newAuthTestServer(&fakeAuthService{loginErr: tc.err})

			rec := serveAuth(mux, http.MethodPost, "/auth/login", `{"username":"alice","password":"secret password"}`, nil)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if got := rec.Header().Get("Retry-After"); got != tc.retryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, tc.retryAfter)
			}
			if strings.Contains(rec.Body.String(), "connection refused") {
				t.Fatal("internal error details were returned")
			}
		})
	}
}

func TestMeReturnsTheSignedInAccount(t *testing.T) {
	mux := newAuthTestServer(&fakeAuthService{sessions: map[string]models.Account{
		"good-token": {ID: 7, Username: "rita", Role: models.RoleReviewer},
	}})

	rec := serveAuth(mux, http.MethodGet, "/auth/me", "", map[string]string{"Authorization": "bearer good-token"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var got accountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != (accountResponse{Username: "rita", Role: models.RoleReviewer}) {
		t.Fatalf("account = %+v", got)
	}
}

func TestSessionEndpointsRejectMissingOrInvalidTokens(t *testing.T) {
	for _, tc := range []struct {
		name          string
		authorization []string
	}{
		{"no header", nil},
		{"unknown token", []string{"Bearer unknown-token"}},
		{"API key scheme", []string{"ApiKey good-token"}},
		{"no token", []string{"Bearer "}},
		{"token with space", []string{"Bearer good-token extra"}},
		{"two headers", []string{"Bearer good-token", "Bearer good-token"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeAuthService{sessions: map[string]models.Account{"good-token": {ID: 7, Username: "rita"}}}
			mux := newAuthTestServer(service)
			for _, path := range []struct{ method, path string }{{http.MethodGet, "/auth/me"}, {http.MethodPost, "/auth/logout"}} {
				req := httptest.NewRequest(path.method, path.path, nil)
				for _, value := range tc.authorization {
					req.Header.Add("Authorization", value)
				}
				rec := httptest.NewRecorder()

				mux.ServeHTTP(rec, req)

				if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
					t.Fatalf("%s %s: status = %d, WWW-Authenticate = %q", path.method, path.path, rec.Code, rec.Header().Get("WWW-Authenticate"))
				}
			}
			if len(service.loggedOut) != 0 {
				t.Fatal("a session was ended")
			}
		})
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	service := &fakeAuthService{sessions: map[string]models.Account{"good-token": {ID: 7, Username: "rita"}}}
	mux := newAuthTestServer(service)

	rec := serveAuth(mux, http.MethodPost, "/auth/logout", "", map[string]string{"Authorization": "Bearer good-token"})

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if len(service.loggedOut) != 1 || service.loggedOut[0] != "good-token" {
		t.Fatalf("logged out = %v, want [good-token]", service.loggedOut)
	}
}
