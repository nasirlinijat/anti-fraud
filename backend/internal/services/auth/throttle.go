package auth

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	maxAttempts   = 5
	attemptWindow = 15 * time.Minute
	// maxTracked bounds the memory used by sign-in attempts for many
	// different usernames. Above it, windows that have ended are dropped.
	maxTracked = 10000
)

// ErrTooManyAttempts means the username has used up its sign-in attempts.
var ErrTooManyAttempts = errors.New("too many sign-in attempts")

// ThrottledError is returned while a username may not sign in. It matches
// ErrTooManyAttempts with errors.Is.
type ThrottledError struct {
	RetryAfter time.Duration
}

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("%v, try again in %v", ErrTooManyAttempts, e.RetryAfter.Round(time.Second))
}

func (e *ThrottledError) Unwrap() error { return ErrTooManyAttempts }

// throttle allows maxAttempts sign-ins per username in each attemptWindow. An
// attempt is counted when it starts, not when it fails, so that parallel
// guesses cannot pass the limit. The counts live in memory, so each backend
// process keeps its own.
type throttle struct {
	mu       sync.Mutex
	now      func() time.Time
	attempts map[string]attempts
}

type attempts struct {
	count int
	since time.Time
}

func newThrottle(now func() time.Time) *throttle {
	return &throttle{now: now, attempts: make(map[string]attempts)}
}

// take counts an attempt for the username and returns zero, or returns how
// long to wait when the username has no attempts left.
func (t *throttle) take(username string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	a := t.attempts[username]
	if now.Sub(a.since) >= attemptWindow {
		a = attempts{since: now}
	}
	if a.count >= maxAttempts {
		return a.since.Add(attemptWindow).Sub(now)
	}
	a.count++
	t.attempts[username] = a
	if len(t.attempts) > maxTracked {
		t.dropEndedWindows(now)
	}
	return 0
}

// reset forgets the attempts of a username after a successful sign-in.
func (t *throttle) reset(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, username)
}

func (t *throttle) dropEndedWindows(now time.Time) {
	for username, a := range t.attempts {
		if now.Sub(a.since) >= attemptWindow {
			delete(t.attempts, username)
		}
	}
}
