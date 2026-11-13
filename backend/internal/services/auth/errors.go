package auth

import "errors"

// ErrInvalidCredentials covers an unknown username, a wrong password, and a
// disabled account alike, so that a failed sign-in reveals nothing about the account.
var ErrInvalidCredentials = errors.New("invalid username or password")

// ErrTooManyAttempts means the username has used up its sign-in attempts.
var ErrTooManyAttempts = errors.New("too many sign-in attempts")
