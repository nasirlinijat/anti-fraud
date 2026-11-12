package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// AccountStore saves console accounts.
type AccountStore struct {
	conn platform.DBTX
}

func NewAccountStore(conn platform.DBTX) *AccountStore {
	return &AccountStore{conn: conn}
}

const insertAccount = `
INSERT INTO accounts (username, password_hash, role)
VALUES ($1, $2, $3)
RETURNING id, username, role, disabled_at, created_at`

// Create saves a new account. It returns models.ErrUsernameTaken when the
// username is already in use.
func (s *AccountStore) Create(ctx context.Context, account models.NewAccount) (models.Account, error) {
	var saved models.Account
	err := s.conn.QueryRow(ctx, insertAccount, account.Username, account.PasswordHash, string(account.Role)).
		Scan(&saved.ID, &saved.Username, &saved.Role, &saved.DisabledAt, &saved.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "accounts_username_key" {
		return models.Account{}, models.ErrUsernameTaken
	}
	if err != nil {
		return models.Account{}, fmt.Errorf("insert account: %w", err)
	}
	saved.CreatedAt = saved.CreatedAt.UTC()
	return saved, nil
}

const getCredentials = `
SELECT id, username, role, disabled_at, created_at, password_hash
FROM accounts
WHERE username = $1`

// Credentials returns the account with the username and its password hash. It
// returns models.ErrAccountNotFound when no account has the username.
func (s *AccountStore) Credentials(ctx context.Context, username string) (models.Account, string, error) {
	var account models.Account
	var hash string
	err := s.conn.QueryRow(ctx, getCredentials, username).
		Scan(&account.ID, &account.Username, &account.Role, &account.DisabledAt, &account.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Account{}, "", models.ErrAccountNotFound
	}
	if err != nil {
		return models.Account{}, "", fmt.Errorf("get credentials of %s: %w", username, err)
	}
	account.CreatedAt = account.CreatedAt.UTC()
	return account, hash, nil
}
