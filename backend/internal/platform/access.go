package platform

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
)

// Roles of console accounts, as saved in accounts.role.
const (
	RoleReviewer = "reviewer"
	RoleAdmin    = "admin"
)

// Identity is the signed-in account behind a session.
type Identity struct {
	Username string
	Role     string
}

// ErrNoSession means the session token is unknown, expired, or ended, or that
// its account is disabled.
var ErrNoSession = errors.New("no session")

// Sessions resolves a session token to the signed-in account.
type Sessions interface {
	Identify(ctx context.Context, token string) (Identity, error)
}

// Access checks the signed-in account on console endpoints.
type Access struct{ Sessions Sessions }

// ConfigRead accepts an admin session.
func (a Access) ConfigRead(next http.Handler) http.Handler {
	return a.Admin(func(w http.ResponseWriter, r *http.Request, _ string) { next.ServeHTTP(w, r) })
}

// ConfigWrite accepts an admin session and supplies its username for auditing.
func (a Access) ConfigWrite(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { a.requireRole(w, r, []string{RoleAdmin}, next) }
}

// Actor is the signed-in account making a review request.
type Actor struct{ ID string }

// Review accepts reviewer and admin sessions.
func (a Access) Review(next func(http.ResponseWriter, *http.Request, Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.requireRole(w, r, []string{RoleReviewer, RoleAdmin}, func(w http.ResponseWriter, r *http.Request, username string) { next(w, r, Actor{ID: username}) })
	}
}

// Admin accepts an admin session and supplies its username.
func (a Access) Admin(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { a.requireRole(w, r, []string{RoleAdmin}, next) }
}

// requireRole passes the username of a valid session with one of the roles to
// next. It answers 401 for a missing or invalid session and 403 for a valid
// session with another role.
func (a Access) requireRole(w http.ResponseWriter, r *http.Request, roles []string, next func(http.ResponseWriter, *http.Request, string)) {
	w.Header().Set("Cache-Control", "no-store")
	token, ok := BearerToken(r)
	if !ok || a.Sessions == nil {
		WriteSessionRequired(w)
		return
	}
	identity, err := a.Sessions.Identify(r.Context(), token)
	if errors.Is(err, ErrNoSession) {
		WriteSessionRequired(w)
		return
	}
	if err != nil {
		WriteServerError(r.Context(), w, "identify session", err)
		return
	}
	if !slices.Contains(roles, identity.Role) {
		WriteError(w, http.StatusForbidden, "your account role cannot use this endpoint")
		return
	}
	next(w, r, identity.Username)
}

// BearerToken reads the token from a single "Authorization: Bearer <token>"
// header. The scheme name is case-insensitive, as HTTP defines it.
func BearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

// WriteSessionRequired answers 401 for a missing or invalid session.
func WriteSessionRequired(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	WriteError(w, http.StatusUnauthorized, "missing or invalid session")
}
