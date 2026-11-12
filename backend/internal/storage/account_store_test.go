package storage

import (
	"context"
	"errors"
	"testing"

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
