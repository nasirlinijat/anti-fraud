package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
)

type fakeStore struct {
	saved []models.NewAccount
	err   error
}

func (f *fakeStore) Create(_ context.Context, account models.NewAccount) (models.Account, error) {
	if f.err != nil {
		return models.Account{}, f.err
	}
	f.saved = append(f.saved, account)
	return models.Account{ID: 1, Username: account.Username, Role: account.Role}, nil
}

func TestCreateSavesLowercaseUsernameAndBcryptHash(t *testing.T) {
	store := &fakeStore{}

	got, err := NewService(store).Create(context.Background(), NewAccount{
		Username: "  Alice.Admin ", Password: "correct horse battery", Role: models.RoleAdmin,
	})

	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Username != "alice.admin" || got.Role != models.RoleAdmin {
		t.Fatalf("account = %+v, want alice.admin as admin", got)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved %d accounts, want 1", len(store.saved))
	}
	hash := store.saved[0].PasswordHash
	if strings.Contains(hash, "correct horse") {
		t.Fatal("password was saved in plain text")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse battery")); err != nil {
		t.Fatalf("saved hash does not match the password: %v", err)
	}
}

func TestCreateRejectsInvalidRequestsWithoutSaving(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  NewAccount
		want error
	}{
		{"short username", NewAccount{Username: "al", Password: "long enough password", Role: models.RoleReviewer}, ErrInvalidUsername},
		{"username with space", NewAccount{Username: "al ice", Password: "long enough password", Role: models.RoleReviewer}, ErrInvalidUsername},
		{"username starting with dot", NewAccount{Username: ".alice", Password: "long enough password", Role: models.RoleReviewer}, ErrInvalidUsername},
		{"unknown role", NewAccount{Username: "alice", Password: "long enough password", Role: "owner"}, ErrInvalidRole},
		{"short password", NewAccount{Username: "alice", Password: "elevenchars", Role: models.RoleReviewer}, ErrPasswordLength},
		{"password over 72 bytes", NewAccount{Username: "alice", Password: strings.Repeat("a", 73), Role: models.RoleReviewer}, ErrPasswordLength},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}

			_, err := NewService(store).Create(context.Background(), tc.req)

			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(store.saved) != 0 {
				t.Fatal("invalid account was saved")
			}
		})
	}
}

func TestCreateReturnsTakenUsername(t *testing.T) {
	store := &fakeStore{err: models.ErrUsernameTaken}

	_, err := NewService(store).Create(context.Background(), NewAccount{
		Username: "alice", Password: "long enough password", Role: models.RoleReviewer,
	})

	if !errors.Is(err, models.ErrUsernameTaken) {
		t.Fatalf("err = %v, want ErrUsernameTaken", err)
	}
}
