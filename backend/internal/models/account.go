package models

import (
	"errors"
	"time"
)

// Role decides which endpoints an account may use.
type Role string

const (
	RoleReviewer Role = "reviewer"
	RoleAdmin    Role = "admin"
)

// Account is a person who signs in to the console.
type Account struct {
	ID       int64
	Username string
	Role     Role
	// DisabledAt is nil while the account may sign in.
	DisabledAt *time.Time
	CreatedAt  time.Time
}

// NewAccount holds what is saved for a new account. PasswordHash is a bcrypt
// hash, never the password itself.
type NewAccount struct {
	Username     string
	PasswordHash string
	Role         Role
}

var (
	// ErrUsernameTaken means another account already has the username.
	ErrUsernameTaken = errors.New("username is already taken")
	// ErrAccountNotFound means no account has the username.
	ErrAccountNotFound = errors.New("account not found")
	// ErrSessionNotFound means the session token is unknown, expired, or belongs
	// to a disabled account.
	ErrSessionNotFound = errors.New("session not found")
)

// Session is a signed-in account. Token is returned once, at sign-in; only
// its hash is saved.
type Session struct {
	Token     string
	ExpiresAt time.Time
	Account   Account
}
