package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

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

const listAccounts = `
SELECT id, username, role, disabled_at, created_at
FROM accounts
ORDER BY username`

// List returns every account, disabled ones included, ordered by username.
func (s *AccountStore) List(ctx context.Context) ([]models.Account, error) {
	rows, err := s.conn.Query(ctx, listAccounts)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	accounts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.Account, error) {
		var a models.Account
		err := row.Scan(&a.ID, &a.Username, &a.Role, &a.DisabledAt, &a.CreatedAt)
		a.CreatedAt = a.CreatedAt.UTC()
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return accounts, nil
}

// disableAccount disables the account, unless it already is, and ends all of
// its sessions in the same statement. Sessions are ended even for an account
// that was already disabled, so that repeating the request is safe.
const disableAccount = `
WITH target AS (
    SELECT id FROM accounts WHERE username = $1
), disabled AS (
    UPDATE accounts a SET disabled_at = now()
    FROM target
    WHERE a.id = target.id AND a.disabled_at IS NULL
), ended AS (
    DELETE FROM sessions s USING target WHERE s.account_id = target.id
)
SELECT a.id, a.username, a.role, COALESCE(a.disabled_at, now()), a.created_at
FROM accounts a
JOIN target ON target.id = a.id`

// Disable disables the account with the username and ends its sessions. It
// returns the account as disabled, or models.ErrAccountNotFound.
func (s *AccountStore) Disable(ctx context.Context, username string) (models.Account, error) {
	var a models.Account
	var disabledAt time.Time
	err := s.conn.QueryRow(ctx, disableAccount, username).Scan(&a.ID, &a.Username, &a.Role, &disabledAt, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Account{}, models.ErrAccountNotFound
	}
	if err != nil {
		return models.Account{}, fmt.Errorf("disable account %s: %w", username, err)
	}
	disabledAt = disabledAt.UTC()
	a.DisabledAt = &disabledAt
	a.CreatedAt = a.CreatedAt.UTC()
	return a, nil
}
