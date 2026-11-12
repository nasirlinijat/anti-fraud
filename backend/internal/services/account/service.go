package account

import (
	"context"
	"errors"
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

var (
	ErrInvalidUsername = errors.New("username must be 3 to 64 lowercase letters, digits, dots, hyphens or underscores, starting with a letter or digit")
	ErrInvalidRole     = errors.New("role must be reviewer or admin")
	ErrPasswordLength  = fmt.Errorf("password must be %d to %d bytes long", MinPasswordLength, maxPasswordLength)
)

// Store saves accounts.
type Store interface {
	Create(ctx context.Context, account models.NewAccount) (models.Account, error)
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
	username := strings.ToLower(strings.TrimSpace(req.Username))
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
