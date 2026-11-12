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

// Access holds every credential that the console endpoints accept. A request
// with an Authorization header is checked as a session and must have an
// allowed role. Any other request is checked by key, as it was before accounts
// existed, so that clients using the keys keep working.
type Access struct {
	ServiceKey string
	Analysts   AnalystKeys
	Operators  OperatorKeys
	Sessions   Sessions
}

// ConfigRead accepts the service key or an admin session.
func (a Access) ConfigRead(next http.Handler) http.Handler {
	byKey := RequireAPIKey(a.ServiceKey, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasAuthorization(r) {
			byKey.ServeHTTP(w, r)
			return
		}
		a.requireRole(w, r, []string{RoleAdmin}, func(w http.ResponseWriter, r *http.Request, _ string) {
			next.ServeHTTP(w, r)
		})
	})
}

// ConfigWrite accepts an operator key or an admin session. It passes the
// operator ID or the username to next, to be saved as the actor.
func (a Access) ConfigWrite(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	byKey := RequireOperator(a.Operators, next)
	return func(w http.ResponseWriter, r *http.Request) {
		if !hasAuthorization(r) {
			byKey(w, r)
			return
		}
		a.requireRole(w, r, []string{RoleAdmin}, next)
	}
}

// Actor is who made a review request.
type Actor struct {
	// ID is the username of a session or the analyst ID of a key. It is saved
	// with review decisions.
	ID string
	// Session is true for a signed-in account, which can claim purchases, and
	// false for an analyst key.
	Session bool
}

// Review accepts an analyst key, or a reviewer or admin session, and passes
// the actor to next.
func (a Access) Review(next func(http.ResponseWriter, *http.Request, Actor)) http.HandlerFunc {
	byKey := RequireAnalyst(a.Analysts, func(w http.ResponseWriter, r *http.Request, analystID string) {
		next(w, r, Actor{ID: analystID})
	})
	return func(w http.ResponseWriter, r *http.Request) {
		if !hasAuthorization(r) {
			byKey(w, r)
			return
		}
		a.requireRole(w, r, []string{RoleReviewer, RoleAdmin}, func(w http.ResponseWriter, r *http.Request, username string) {
			next(w, r, Actor{ID: username, Session: true})
		})
	}
}

// Admin accepts only an admin session, with no key in its place. It passes
// the username to next.
func (a Access) Admin(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.requireRole(w, r, []string{RoleAdmin}, next)
	}
}

func hasAuthorization(r *http.Request) bool {
	return len(r.Header.Values("Authorization")) > 0
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

// RequireAnalyst authenticates review requests by analyst key and supplies the analyst ID.
func RequireAnalyst(keys AnalystKeys, next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !keys.Configured() {
			WriteError(w, http.StatusServiceUnavailable, "review access is not configured")
			return
		}
		if values := r.Header.Values(APIKeyHeader); len(values) == 1 {
			if analystID := keys.Analyst(values[0]); analystID != "" {
				next(w, r, analystID)
				return
			}
		}
		WriteError(w, http.StatusUnauthorized, "missing or invalid analyst key")
	}
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
