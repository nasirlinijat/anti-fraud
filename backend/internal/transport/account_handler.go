package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/account"
)

const maxAccountBytes = 1024

// AccountService creates, lists, and disables console accounts.
type AccountService interface {
	Create(ctx context.Context, req account.NewAccount) (models.Account, error)
	List(ctx context.Context) ([]models.Account, error)
	Disable(ctx context.Context, username, actor string) (models.Account, error)
}

// AccountHandler serves account management to admins.
type AccountHandler struct {
	service AccountService
}

func NewAccountHandler(service AccountService) *AccountHandler {
	return &AccountHandler{service: service}
}

// RegisterAccountRoutes registers the account management endpoints, which
// only an admin session can use.
func RegisterAccountRoutes(mux *http.ServeMux, h *AccountHandler, access platform.Access) {
	mux.HandleFunc("POST /admin/accounts", access.Admin(h.serveCreate))
	mux.HandleFunc("GET /admin/accounts", access.Admin(h.serveList))
	mux.HandleFunc("POST /admin/accounts/{username}/disable", access.Admin(h.serveDisable))
}

// accountDetailsResponse is an account as admins see it. It never carries the
// password hash.
type accountDetailsResponse struct {
	Username  string      `json:"username"`
	Role      models.Role `json:"role"`
	Disabled  bool        `json:"disabled"`
	CreatedAt time.Time   `json:"created_at"`
}

type accountListResponse struct {
	Accounts []accountDetailsResponse `json:"accounts"`
}

type createAccountRequest struct {
	Username string      `json:"username"`
	Password string      `json:"password"`
	Role     models.Role `json:"role"`
}

func (h *AccountHandler) serveCreate(w http.ResponseWriter, r *http.Request, _ string) {
	req, ok := readCreateAccount(w, r)
	if !ok {
		return
	}
	created, err := h.service.Create(r.Context(), account.NewAccount{Username: req.Username, Password: req.Password, Role: req.Role})
	if err != nil {
		writeAccountError(w, err, "create account")
		return
	}
	platform.WriteJSON(w, http.StatusCreated, newAccountDetailsResponse(created))
}

// readCreateAccount reads the account body. On failure it writes the error
// response and returns false.
func readCreateAccount(w http.ResponseWriter, r *http.Request) (createAccountRequest, bool) {
	var req createAccountRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAccountBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(&req)
	if err == nil && dec.Decode(new(any)) != io.EOF {
		err = errors.New("multiple JSON values")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		platform.WriteError(w, http.StatusRequestEntityTooLarge, "account body is too large")
	case err != nil:
		platform.WriteError(w, http.StatusBadRequest, "request body must be a single account object")
	default:
		return req, true
	}
	return createAccountRequest{}, false
}

func (h *AccountHandler) serveList(w http.ResponseWriter, r *http.Request, _ string) {
	accounts, err := h.service.List(r.Context())
	if err != nil {
		writeAccountError(w, err, "list accounts")
		return
	}
	list := make([]accountDetailsResponse, 0, len(accounts))
	for _, a := range accounts {
		list = append(list, newAccountDetailsResponse(a))
	}
	platform.WriteJSON(w, http.StatusOK, accountListResponse{Accounts: list})
}

func (h *AccountHandler) serveDisable(w http.ResponseWriter, r *http.Request, admin string) {
	disabled, err := h.service.Disable(r.Context(), r.PathValue("username"), admin)
	if err != nil {
		writeAccountError(w, err, "disable account")
		return
	}
	platform.WriteJSON(w, http.StatusOK, newAccountDetailsResponse(disabled))
}

// writeAccountError maps the account service errors to status codes. A
// validation error names the request field it is about. Any other error is
// logged with the action that failed and answered with 500.
func writeAccountError(w http.ResponseWriter, err error, action string) {
	field := map[error]string{
		account.ErrInvalidUsername: "username",
		account.ErrInvalidRole:     "role",
		account.ErrPasswordLength:  "password",
	}
	for target, name := range field {
		if errors.Is(err, target) {
			platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid account", Fields: map[string]string{name: err.Error()}})
			return
		}
	}
	switch {
	case errors.Is(err, models.ErrAccountNotFound):
		platform.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, models.ErrUsernameTaken), errors.Is(err, account.ErrDisableSelf):
		platform.WriteError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("%s: %v", action, err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
	}
}

func newAccountDetailsResponse(a models.Account) accountDetailsResponse {
	return accountDetailsResponse{Username: a.Username, Role: a.Role, Disabled: a.DisabledAt != nil, CreatedAt: a.CreatedAt.UTC()}
}
