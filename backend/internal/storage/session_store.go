package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// SessionStore saves sign-in sessions by the SHA-256 hash of their token.
type SessionStore struct {
	conn platform.DBTX
}

func NewSessionStore(conn platform.DBTX) *SessionStore {
	return &SessionStore{conn: conn}
}

// insertSession also removes the account's expired sessions, so that the
// table does not grow with every sign-in. Expiry uses the database clock, the
// same clock that checks it.
const insertSession = `
WITH expired AS (
    DELETE FROM sessions WHERE account_id = $2 AND expires_at <= now()
)
INSERT INTO sessions (token_hash, account_id, expires_at)
VALUES ($1, $2, now() + make_interval(secs => $3))
RETURNING expires_at`

// Create saves a session that lasts for ttl and returns its expiry time in UTC.
func (s *SessionStore) Create(ctx context.Context, tokenHash []byte, accountID int64, ttl time.Duration) (time.Time, error) {
	var expiresAt time.Time
	if err := s.conn.QueryRow(ctx, insertSession, tokenHash, accountID, ttl.Seconds()).Scan(&expiresAt); err != nil {
		return time.Time{}, fmt.Errorf("insert session: %w", err)
	}
	return expiresAt.UTC(), nil
}

const getSessionAccount = `
SELECT a.id, a.username, a.role, a.disabled_at, a.created_at
FROM sessions s
JOIN accounts a ON a.id = s.account_id
WHERE s.token_hash = $1 AND s.expires_at > now() AND a.disabled_at IS NULL`

// Account returns the account signed in with the session. It returns
// models.ErrSessionNotFound for an unknown or expired session, and for a
// session of a disabled account.
func (s *SessionStore) Account(ctx context.Context, tokenHash []byte) (models.Account, error) {
	var account models.Account
	err := s.conn.QueryRow(ctx, getSessionAccount, tokenHash).
		Scan(&account.ID, &account.Username, &account.Role, &account.DisabledAt, &account.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Account{}, models.ErrSessionNotFound
	}
	if err != nil {
		return models.Account{}, fmt.Errorf("get session account: %w", err)
	}
	account.CreatedAt = account.CreatedAt.UTC()
	return account, nil
}

const deleteSession = `DELETE FROM sessions WHERE token_hash = $1`

// Delete ends the session. Deleting an unknown session is not an error.
func (s *SessionStore) Delete(ctx context.Context, tokenHash []byte) error {
	if _, err := s.conn.Exec(ctx, deleteSession, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
