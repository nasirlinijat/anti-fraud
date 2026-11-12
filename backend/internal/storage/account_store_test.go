package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

func TestCreateAccountSavesAReviewer(t *testing.T) {
	tx := testTx(t)

	got, err := NewAccountStore(tx).Create(context.Background(), models.NewAccount{
		Username: "store-test-reviewer", PasswordHash: "$2a$12$hash", Role: models.RoleReviewer,
	})

	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID <= 0 || got.Username != "store-test-reviewer" || got.Role != models.RoleReviewer || got.DisabledAt != nil || got.CreatedAt.IsZero() {
		t.Fatalf("account = %+v", got)
	}
}

func TestCreateAccountRejectsTakenUsername(t *testing.T) {
	tx := testTx(t)
	store := NewAccountStore(tx)
	account := models.NewAccount{Username: "store-test-taken", PasswordHash: "$2a$12$hash", Role: models.RoleAdmin}
	if _, err := store.Create(context.Background(), account); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err := store.Create(context.Background(), account)

	if !errors.Is(err, models.ErrUsernameTaken) {
		t.Fatalf("err = %v, want ErrUsernameTaken", err)
	}
}

func TestCredentialsReturnsTheAccountAndHash(t *testing.T) {
	tx := testTx(t)
	created := createTestAccount(t, tx, "credentials-test")

	account, hash, err := NewAccountStore(tx).Credentials(context.Background(), "credentials-test")

	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if account.ID != created.ID || hash != "$2a$12$hash" {
		t.Fatalf("got %+v with hash %q", account, hash)
	}
}

func TestCredentialsOfUnknownUsername(t *testing.T) {
	tx := testTx(t)

	_, _, err := NewAccountStore(tx).Credentials(context.Background(), "credentials-test-missing")

	if !errors.Is(err, models.ErrAccountNotFound) {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestDisableAccountEndsItsSessionsOnce(t *testing.T) {
	tx := testTx(t)
	account := createTestAccount(t, tx, "disable-test-rita")
	other := createTestAccount(t, tx, "disable-test-sam")
	sessions := NewSessionStore(tx)
	for _, s := range []struct {
		token string
		id    int64
	}{{"rita-phone", account.ID}, {"rita-laptop", account.ID}, {"sam-laptop", other.ID}} {
		if _, err := sessions.Create(context.Background(), tokenHash(s.token), s.id, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	store := NewAccountStore(tx)

	first, err := store.Disable(context.Background(), "disable-test-rita")
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	again, err := store.Disable(context.Background(), "disable-test-rita")

	if err != nil || first.DisabledAt == nil || again.DisabledAt == nil || !again.DisabledAt.Equal(*first.DisabledAt) {
		t.Fatalf("disabled at %v then %v, err %v; want the same time", first.DisabledAt, again.DisabledAt, err)
	}
	for _, token := range []string{"rita-phone", "rita-laptop"} {
		if _, err := sessions.Account(context.Background(), tokenHash(token)); !errors.Is(err, models.ErrSessionNotFound) {
			t.Fatalf("%s: err = %v, want ErrSessionNotFound", token, err)
		}
	}
	var left int
	if err := tx.QueryRow(context.Background(), `SELECT count(*) FROM sessions WHERE account_id = $1`, account.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rita has %d sessions left, err %v", left, err)
	}
	if _, err := sessions.Account(context.Background(), tokenHash("sam-laptop")); err != nil {
		t.Fatalf("other account's session: %v", err)
	}
}

func TestDisableUnknownAccount(t *testing.T) {
	tx := testTx(t)

	_, err := NewAccountStore(tx).Disable(context.Background(), "disable-test-missing")

	if !errors.Is(err, models.ErrAccountNotFound) {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestListAccountsIncludesDisabledOnes(t *testing.T) {
	tx := testTx(t)
	createTestAccount(t, tx, "list-test-active")
	createTestAccount(t, tx, "list-test-disabled")
	store := NewAccountStore(tx)
	if _, err := store.Disable(context.Background(), "list-test-disabled"); err != nil {
		t.Fatal(err)
	}

	accounts, err := store.List(context.Background())

	if err != nil {
		t.Fatalf("List: %v", err)
	}
	disabled := map[string]bool{}
	for _, a := range accounts {
		disabled[a.Username] = a.DisabledAt != nil
	}
	if active, ok := disabled["list-test-active"]; !ok || active {
		t.Fatalf("list-test-active missing or disabled: %v", disabled)
	}
	if off, ok := disabled["list-test-disabled"]; !ok || !off {
		t.Fatalf("list-test-disabled missing or active: %v", disabled)
	}
}
