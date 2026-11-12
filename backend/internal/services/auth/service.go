package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"golang.org/x/crypto/bcrypt"
)

// SessionTTL is how long a session lasts after sign-in.
const SessionTTL = 12 * time.Hour

const tokenBytes = 32

// unknownAccountHash is a cost-12 bcrypt hash of a random password. Comparing
// against it for an unknown username makes that sign-in take as long as one
// with a wrong password, so the response time does not reveal which usernames exist.
const unknownAccountHash = "$2a$12$5QnowLIxTIQdmUyo6vxuG.3xBRGj5WSQoCiGaQr0AvqzZNbkBGY/W"

// ErrInvalidCredentials covers an unknown username, a wrong password, and a
// disabled account alike, so that a failed sign-in reveals nothing about the account.
var ErrInvalidCredentials = errors.New("invalid username or password")

// Accounts reads the credentials saved for a username.
type Accounts interface {
	Credentials(ctx context.Context, username string) (models.Account, string, error)
}

// Sessions saves sessions by the SHA-256 hash of their token.
type Sessions interface {
	Create(ctx context.Context, tokenHash []byte, accountID int64, ttl time.Duration) (time.Time, error)
	Account(ctx context.Context, tokenHash []byte) (models.Account, error)
	Delete(ctx context.Context, tokenHash []byte) error
}

// Service signs accounts in and out and resolves session tokens.
type Service struct {
	accounts Accounts
	sessions Sessions
	attempts *throttle
}

func NewService(accounts Accounts, sessions Sessions) *Service {
	return &Service{accounts: accounts, sessions: sessions, attempts: newThrottle(time.Now)}
}

// Login checks the password and starts a session. It returns
// ErrInvalidCredentials for any failed check, and a *ThrottledError while the
// username has used up its sign-in attempts.
func (s *Service) Login(ctx context.Context, username, password string) (models.Session, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if wait := s.attempts.take(username); wait > 0 {
		return models.Session{}, &ThrottledError{RetryAfter: wait}
	}
	account, err := s.checkPassword(ctx, username, password)
	if err != nil {
		return models.Session{}, err
	}
	s.attempts.reset(username)
	return s.startSession(ctx, account)
}

// checkPassword compares the password before it looks at the disabled flag, so
// that a disabled account takes as long to reject as a wrong password.
func (s *Service) checkPassword(ctx context.Context, username, password string) (models.Account, error) {
	account, hash, err := s.accounts.Credentials(ctx, username)
	if errors.Is(err, models.ErrAccountNotFound) {
		_ = bcrypt.CompareHashAndPassword([]byte(unknownAccountHash), []byte(password))
		return models.Account{}, ErrInvalidCredentials
	}
	if err != nil {
		return models.Account{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || account.DisabledAt != nil {
		return models.Account{}, ErrInvalidCredentials
	}
	return account, nil
}

func (s *Service) startSession(ctx context.Context, account models.Account) (models.Session, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return models.Session{}, fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expiresAt, err := s.sessions.Create(ctx, hashToken(token), account.ID, SessionTTL)
	if err != nil {
		return models.Session{}, err
	}
	return models.Session{Token: token, ExpiresAt: expiresAt, Account: account}, nil
}

// Authenticate returns the account signed in with the token. It returns
// models.ErrSessionNotFound for an unknown, expired, or ended session.
func (s *Service) Authenticate(ctx context.Context, token string) (models.Account, error) {
	if token == "" {
		return models.Account{}, models.ErrSessionNotFound
	}
	return s.sessions.Account(ctx, hashToken(token))
}

// Identify returns the username and role of the session, for the access
// checks in platform. It returns platform.ErrNoSession for a session that
// Authenticate rejects.
func (s *Service) Identify(ctx context.Context, token string) (platform.Identity, error) {
	account, err := s.Authenticate(ctx, token)
	if errors.Is(err, models.ErrSessionNotFound) {
		return platform.Identity{}, platform.ErrNoSession
	}
	if err != nil {
		return platform.Identity{}, err
	}
	return platform.Identity{Username: account.Username, Role: string(account.Role)}, nil
}

// Logout ends the session. Ending an unknown session is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.sessions.Delete(ctx, hashToken(token))
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
