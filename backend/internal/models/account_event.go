package models

import (
	"errors"
	"time"
)

// AccountEventType is the kind of activity on a player's game account.
type AccountEventType string

const (
	AccountEventLogin         AccountEventType = "login"
	AccountEventPasswordReset AccountEventType = "password_reset"
)

// AccountEventTypes lists every account event type.
var AccountEventTypes = []AccountEventType{AccountEventLogin, AccountEventPasswordReset}

// AccountEvent is a login or password reset that the game's account system
// reports for a player. Optional fields are nil when the request omits them.
// Account events feed rules only, never model inputs.
type AccountEvent struct {
	EventID   string
	PlayerID  string
	EventType AccountEventType
	EventTime time.Time
	DeviceID  *string
	IPCountry *string
	// ReceivedAt is when the service recorded the event. It is zero until the
	// event is saved and is set only when a saved event is read.
	ReceivedAt time.Time
}

// ErrDuplicateAccountEvent means an account event with the same ID and
// different fields is already saved.
var ErrDuplicateAccountEvent = errors.New("account event ID is already saved")
