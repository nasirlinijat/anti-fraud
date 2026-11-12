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

// ErrUsernameTaken means another account already has the username.
var ErrUsernameTaken = errors.New("username is already taken")
