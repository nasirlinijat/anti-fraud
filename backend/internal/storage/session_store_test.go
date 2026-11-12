package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

func createTestAccount(t *testing.T, tx pgx.Tx, username string) models.Account {
	t.Helper()
	account, err := NewAccountStore(tx).Create(context.Background(), models.NewAccount{
		Username: username, PasswordHash: "$2a$12$hash", Role: models.RoleReviewer,
	})
	if err != nil {
		t.Fatalf("create account %s: %v", username, err)
	}
	return account
}

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func TestSessionAccountReturnsTheSignedInAccount(t *testing.T) {
	tx := testTx(t)
	account := createTestAccount(t, tx, "session-test-active")
	sessions := NewSessionStore(tx)
	expiresAt, err := sessions.Create(context.Background(), tokenHash("active"), account.ID, time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := sessions.Account(context.Background(), tokenHash("active"))

	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if got.ID != account.ID || got.Username != "session-test-active" {
		t.Fatalf("account = %+v, want %+v", got, account)
	}
	if until := time.Until(expiresAt); until < 59*time.Minute || until > 61*time.Minute {
		t.Fatalf("session expires in %v, want about an hour", until)
	}
}

func TestSessionAccountRejectsEndedSessions(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  string
	}{
		{"expired", `UPDATE sessions SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour' WHERE token_hash = $1`},
		{"deleted", `DELETE FROM sessions WHERE token_hash = $1`},
		{"disabled account", `UPDATE accounts SET disabled_at = now() WHERE id = (SELECT account_id FROM sessions WHERE token_hash = $1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testTx(t)
			account := createTestAccount(t, tx, "session-test-ended")
			sessions := NewSessionStore(tx)
			if _, err := sessions.Create(context.Background(), tokenHash("ended"), account.ID, time.Hour); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := tx.Exec(context.Background(), tc.end, tokenHash("ended")); err != nil {
				t.Fatalf("end session: %v", err)
			}

			_, err := sessions.Account(context.Background(), tokenHash("ended"))

			if !errors.Is(err, models.ErrSessionNotFound) {
				t.Fatalf("err = %v, want ErrSessionNotFound", err)
			}
		})
	}
}

func TestDeleteSessionEndsOnlyThatSession(t *testing.T) {
	tx := testTx(t)
	account := createTestAccount(t, tx, "session-test-logout")
	sessions := NewSessionStore(tx)
	for _, token := range []string{"phone", "laptop"} {
		if _, err := sessions.Create(context.Background(), tokenHash(token), account.ID, time.Hour); err != nil {
			t.Fatalf("Create %s: %v", token, err)
		}
	}

	if err := sessions.Delete(context.Background(), tokenHash("phone")); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := sessions.Account(context.Background(), tokenHash("phone")); !errors.Is(err, models.ErrSessionNotFound) {
		t.Fatalf("deleted session: err = %v, want ErrSessionNotFound", err)
	}
	if _, err := sessions.Account(context.Background(), tokenHash("laptop")); err != nil {
		t.Fatalf("other session: %v", err)
	}
}
