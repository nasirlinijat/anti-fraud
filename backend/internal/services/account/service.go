package account

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
)

const (
	// MinPasswordLength is counted in bytes, like the bcrypt limit below.
	MinPasswordLength = 12
	// bcrypt reads at most 72 bytes, so a longer password would be cut short silently.
	maxPasswordLength = 72
	passwordCost      = 12
)

// usernamePattern matches the check on accounts.username.
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)

// Store saves and reads accounts.
type Store interface {
	Create(ctx context.Context, account models.NewAccount) (models.Account, error)
	List(ctx context.Context) ([]models.Account, error)
	Disable(ctx context.Context, username string) (models.Account, error)
}

// Service creates console accounts.
type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// NewAccount is a request to create an account.
type NewAccount struct {
	Username string
	Password string
	Role     models.Role
}

// Create checks the request, hashes the password, and saves the account. The
// username is saved in lowercase. It returns models.ErrUsernameTaken when the
// username is in use.
func (s *Service) Create(ctx context.Context, req NewAccount) (models.Account, error) {
	username := normalizeUsername(req.Username)
	if !usernamePattern.MatchString(username) {
		return models.Account{}, ErrInvalidUsername
	}
	if req.Role != models.RoleReviewer && req.Role != models.RoleAdmin {
		return models.Account{}, ErrInvalidRole
	}
	if len(req.Password) < MinPasswordLength || len(req.Password) > maxPasswordLength {
		return models.Account{}, ErrPasswordLength
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), passwordCost)
	if err != nil {
		return models.Account{}, fmt.Errorf("hash password: %w", err)
	}
	return s.store.Create(ctx, models.NewAccount{Username: username, PasswordHash: string(hash), Role: req.Role})
}

// List returns every account, disabled ones included.
func (s *Service) List(ctx context.Context) ([]models.Account, error) {
	return s.store.List(ctx)
}

// Disable disables an account and ends its sessions, so that it is signed out
// at once. An admin cannot disable their own account, so that the last admin
// cannot lock everyone out. It returns models.ErrAccountNotFound for an
// unknown username.
func (s *Service) Disable(ctx context.Context, username, actor string) (models.Account, error) {
	username = normalizeUsername(username)
	if username == normalizeUsername(actor) {
		return models.Account{}, ErrDisableSelf
	}
	return s.store.Disable(ctx, username)
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
