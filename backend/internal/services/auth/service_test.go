package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
)

const testPassword = "correct horse battery"

type fakeAccounts struct {
	accounts map[string]models.Account
	hashes   map[string]string
}

func (f *fakeAccounts) Credentials(_ context.Context, username string) (models.Account, string, error) {
	account, ok := f.accounts[username]
	if !ok {
		return models.Account{}, "", models.ErrAccountNotFound
	}
	return account, f.hashes[username], nil
}

type fakeSessions struct {
	saved   map[string]int64
	ttl     time.Duration
	deleted [][]byte
	byID    map[int64]models.Account
}

func (f *fakeSessions) Create(_ context.Context, tokenHash []byte, accountID int64, ttl time.Duration) (time.Time, error) {
	f.saved[string(tokenHash)] = accountID
	f.ttl = ttl
	return time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC), nil
}

func (f *fakeSessions) Account(_ context.Context, tokenHash []byte) (models.Account, error) {
	id, ok := f.saved[string(tokenHash)]
	if !ok {
		return models.Account{}, models.ErrSessionNotFound
	}
	return f.byID[id], nil
}

func (f *fakeSessions) Delete(_ context.Context, tokenHash []byte) error {
	f.deleted = append(f.deleted, tokenHash)
	delete(f.saved, string(tokenHash))
	return nil
}

// newTestService has an active reviewer "alice" and a disabled reviewer
// "bob", both with testPassword, and a clock that the test can move.
func newTestService(t *testing.T) (*Service, *fakeSessions, *time.Time) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	disabledAt := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	alice := models.Account{ID: 1, Username: "alice", Role: models.RoleReviewer}
	bob := models.Account{ID: 2, Username: "bob", Role: models.RoleReviewer, DisabledAt: &disabledAt}
	accounts := &fakeAccounts{
		accounts: map[string]models.Account{"alice": alice, "bob": bob},
		hashes:   map[string]string{"alice": string(hash), "bob": string(hash)},
	}
	sessions := &fakeSessions{saved: map[string]int64{}, byID: map[int64]models.Account{1: alice, 2: bob}}
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	service := NewService(accounts, sessions)
	service.attempts = newThrottle(func() time.Time { return now })
	return service, sessions, &now
}

func TestLoginStartsASessionSavedByTokenHash(t *testing.T) {
	service, sessions, _ := newTestService(t)

	session, err := service.Login(context.Background(), " Alice ", testPassword)

	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.Account.Username != "alice" || len(session.Token) < 43 {
		t.Fatalf("session = %+v", session)
	}
	sum := sha256.Sum256([]byte(session.Token))
	if sessions.saved[string(sum[:])] != 1 || sessions.saved[session.Token] != 0 {
		t.Fatalf("saved sessions = %v, want only the token hash", sessions.saved)
	}
	if sessions.ttl != 12*time.Hour {
		t.Fatalf("ttl = %v, want 12h", sessions.ttl)
	}
}

func TestLoginRejectsInvalidCredentialsAlike(t *testing.T) {
	for _, tc := range []struct {
		name     string
		username string
		password string
	}{
		{"wrong password", "alice", "wrong password here"},
		{"unknown username", "mallory", testPassword},
		{"disabled account", "bob", testPassword},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, sessions, _ := newTestService(t)

			_, err := service.Login(context.Background(), tc.username, tc.password)

			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("err = %v, want ErrInvalidCredentials", err)
			}
			if len(sessions.saved) != 0 {
				t.Fatal("a session was saved")
			}
		})
	}
}

func TestLoginIsThrottledAfterFiveAttemptsUntilTheWindowEnds(t *testing.T) {
	service, _, now := newTestService(t)
	for range 5 {
		if _, err := service.Login(context.Background(), "alice", "wrong password here"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	}
	*now = now.Add(10 * time.Minute)

	_, err := service.Login(context.Background(), "ALICE", testPassword)

	var throttled *ThrottledError
	if !errors.As(err, &throttled) || !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("err = %v, want ThrottledError", err)
	}
	if throttled.RetryAfter != 5*time.Minute {
		t.Fatalf("RetryAfter = %v, want 5m", throttled.RetryAfter)
	}
	*now = now.Add(5 * time.Minute)
	if _, err := service.Login(context.Background(), "alice", testPassword); err != nil {
		t.Fatalf("Login after the window: %v", err)
	}
}

func TestSuccessfulLoginResetsAttempts(t *testing.T) {
	service, _, _ := newTestService(t)
	for round := range 2 {
		for range 4 {
			_, _ = service.Login(context.Background(), "alice", "wrong password here")
		}

		if _, err := service.Login(context.Background(), "alice", testPassword); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}

func TestThrottleCountsEachUsernameSeparately(t *testing.T) {
	service, _, _ := newTestService(t)
	for range 5 {
		_, _ = service.Login(context.Background(), "mallory", testPassword)
	}

	if _, err := service.Login(context.Background(), "alice", testPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestAuthenticateReturnsTheSessionAccount(t *testing.T) {
	service, _, _ := newTestService(t)
	session, err := service.Login(context.Background(), "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}

	account, err := service.Authenticate(context.Background(), session.Token)

	if err != nil || account.Username != "alice" {
		t.Fatalf("Authenticate = %+v, %v", account, err)
	}
}

func TestAuthenticateRejectsEmptyAndUnknownTokens(t *testing.T) {
	service, _, _ := newTestService(t)

	for _, token := range []string{"", "unknown-token"} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, models.ErrSessionNotFound) {
			t.Fatalf("token %q: err = %v, want ErrSessionNotFound", token, err)
		}
	}
}

func TestLogoutDeletesTheSessionByTokenHash(t *testing.T) {
	service, sessions, _ := newTestService(t)
	session, err := service.Login(context.Background(), "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Logout(context.Background(), session.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	sum := sha256.Sum256([]byte(session.Token))
	if len(sessions.deleted) != 1 || !bytes.Equal(sessions.deleted[0], sum[:]) {
		t.Fatalf("deleted = %x, want the token hash", sessions.deleted)
	}
	if _, err := service.Authenticate(context.Background(), session.Token); !errors.Is(err, models.ErrSessionNotFound) {
		t.Fatalf("Authenticate after logout: err = %v", err)
	}
}
