package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/auth"
)

const maxLoginBytes = 1024

// AuthService signs accounts in and out and resolves session tokens.
type AuthService interface {
	Login(ctx context.Context, username, password string) (models.Session, error)
	Authenticate(ctx context.Context, token string) (models.Account, error)
	Logout(ctx context.Context, token string) error
}

// AuthHandler serves sign-in, sign-out, and the signed-in account.
type AuthHandler struct {
	service AuthService
}

func NewAuthHandler(service AuthService) *AuthHandler {
	return &AuthHandler{service: service}
}

// RegisterAuthRoutes registers the sign-in endpoints. Sign-out and the
// signed-in account need a session token in the Authorization header.
func RegisterAuthRoutes(mux *http.ServeMux, h *AuthHandler) {
	mux.HandleFunc("POST /auth/login", h.serveLogin)
	mux.HandleFunc("GET /auth/me", RequireSession(h.service, h.serveMe))
	mux.HandleFunc("POST /auth/logout", RequireSession(h.service, h.serveLogout))
}

// RequireSession passes requests with a valid session token to next, with the
// signed-in account, and rejects all others with 401.
func RequireSession(service AuthService, next func(http.ResponseWriter, *http.Request, models.Account)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := platform.BearerToken(r)
		if !ok {
			platform.WriteSessionRequired(w)
			return
		}
		account, err := service.Authenticate(r.Context(), token)
		if errors.Is(err, models.ErrSessionNotFound) {
			platform.WriteSessionRequired(w)
			return
		}
		if err != nil {
			platform.WriteServerError(r.Context(), w, "authenticate session", err)
			return
		}
		next(w, r, account)
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type accountResponse struct {
	Username string      `json:"username"`
	Role     models.Role `json:"role"`
}

type loginResponse struct {
	Token     string          `json:"token"`
	ExpiresAt time.Time       `json:"expires_at"`
	Account   accountResponse `json:"account"`
}

func (h *AuthHandler) serveLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	req, ok := readLogin(w, r)
	if !ok {
		return
	}
	session, err := h.service.Login(r.Context(), req.Username, req.Password)
	var throttled *auth.ThrottledError
	switch {
	case errors.As(err, &throttled):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.RetryAfter.Seconds()))))
		platform.WriteError(w, http.StatusTooManyRequests, "too many sign-in attempts, try again later")
	case errors.Is(err, auth.ErrInvalidCredentials):
		platform.WriteError(w, http.StatusUnauthorized, err.Error())
	case err != nil:
		platform.WriteServerError(r.Context(), w, "sign in", err)
	default:
		platform.WriteJSON(w, http.StatusOK, loginResponse{
			Token:     session.Token,
			ExpiresAt: session.ExpiresAt.UTC(),
			Account:   newAccountResponse(session.Account),
		})
	}
}

// readLogin reads the sign-in body. On failure it writes the error response
// and returns false.
func readLogin(w http.ResponseWriter, r *http.Request) (loginRequest, bool) {
	var req loginRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(&req)
	if err == nil && dec.Decode(new(any)) != io.EOF {
		err = errors.New("multiple JSON values")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		platform.WriteError(w, http.StatusRequestEntityTooLarge, "sign-in body is too large")
	case err != nil:
		platform.WriteError(w, http.StatusBadRequest, "request body must be a single sign-in object")
	case req.Username == "" || req.Password == "":
		platform.WriteError(w, http.StatusBadRequest, "username and password are required")
	default:
		return req, true
	}
	return loginRequest{}, false
}

func (h *AuthHandler) serveMe(w http.ResponseWriter, _ *http.Request, account models.Account) {
	platform.WriteJSON(w, http.StatusOK, newAccountResponse(account))
}

func (h *AuthHandler) serveLogout(w http.ResponseWriter, r *http.Request, _ models.Account) {
	token, _ := platform.BearerToken(r)
	if err := h.service.Logout(r.Context(), token); err != nil {
		platform.WriteServerError(r.Context(), w, "sign out", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func newAccountResponse(account models.Account) accountResponse {
	return accountResponse{Username: account.Username, Role: account.Role}
}
