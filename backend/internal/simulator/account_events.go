package simulator

import "time"

// accountEventStream seeds the random stream of account events. It is
// separate from the purchase stream, so the account events never change the
// purchases of a seed.
const accountEventStream = 0xacc0

// Shares of purchases that the game's account system reports account events
// for before the purchase.
const (
	// regularLoginShare is the share of normal purchases that follow a login
	// from the player's own device and country. These logins are the history
	// that the takeover rules compare later logins with.
	regularLoginShare = 0.3
	// regularResetShare is the share of normal purchases whose player reset
	// their password a few hours to two days earlier.
	regularResetShare = 0.01
	// takeoverResetShare is the share of takeovers that start with a password
	// reset shortly before the purchase.
	takeoverResetShare = 0.7
	// travelerLoginShare is the share of traveler purchases that follow a
	// login from abroad.
	travelerLoginShare = 0.8
)

// Account event types, as POST /account-events takes them.
const (
	accountLogin         = "login"
	accountPasswordReset = "password_reset"
)

// AccountEvent is a login or password reset in the API format of
// POST /account-events.
type AccountEvent struct {
	EventID   string    `json:"event_id"`
	PlayerID  string    `json:"player_id"`
	EventType string    `json:"event_type"`
	EventTime time.Time `json:"event_time"`
	DeviceID  *string   `json:"device_id,omitempty"`
	IPCountry *string   `json:"ip_country,omitempty"`
}

// accountEvents returns the account events the game reports before the
// purchase, oldest first. They use only the account event stream and take
// the device and country from the finished request, so a player whose client
// leaves those fields out sends account events without them too.
func (g *generator) accountEvents(pattern Pattern, req Request) []AccountEvent {
	switch pattern {
	case PatternNormal:
		return g.regularAccountEvents(req)
	case PatternAccountTakeover, PatternQuietTakeover:
		return g.takeoverAccountEvents(req)
	case PatternNewPhone:
		return []AccountEvent{g.accountEvent(req, accountLogin, g.accountMinutes(1, 30))}
	case PatternTraveler:
		if g.accountRng.Float64() < travelerLoginShare {
			return []AccountEvent{g.accountEvent(req, accountLogin, g.accountMinutes(1, 60))}
		}
	}
	return nil
}

// regularAccountEvents sometimes adds a login from the player's own device
// and country, and rarely a password reset well before it.
func (g *generator) regularAccountEvents(req Request) []AccountEvent {
	var events []AccountEvent
	if g.accountRng.Float64() < regularResetShare {
		events = append(events, g.accountEvent(req, accountPasswordReset, g.accountMinutes(120, 2880)))
	}
	if g.accountRng.Float64() < regularLoginShare {
		events = append(events, g.accountEvent(req, accountLogin, g.accountMinutes(1, 60)))
	}
	return events
}

// takeoverAccountEvents is how a taken-over account looks to the account
// system: often a password reset, then a login from the new device, both
// shortly before the purchase.
func (g *generator) takeoverAccountEvents(req Request) []AccountEvent {
	var events []AccountEvent
	if g.accountRng.Float64() < takeoverResetShare {
		events = append(events, g.accountEvent(req, accountPasswordReset, g.accountMinutes(20, 120)))
	}
	return append(events, g.accountEvent(req, accountLogin, g.accountMinutes(1, 15)))
}

// accountEvent returns an event of the request's player the given time
// before the purchase. Its ID is derived from the transaction ID, never from
// the shared ID counter, so the purchase IDs stay the same.
func (g *generator) accountEvent(req Request, eventType string, before time.Duration) AccountEvent {
	return AccountEvent{
		EventID:   req.TransactionID + "-" + eventType,
		PlayerID:  req.PlayerID,
		EventType: eventType,
		EventTime: req.EventTime.Add(-before),
		DeviceID:  copyOf(req.DeviceID),
		IPCountry: copyOf(req.IPCountry),
	}
}

func (g *generator) accountMinutes(low, high int) time.Duration {
	return time.Duration(low+g.accountRng.IntN(high-low+1)) * time.Minute
}

// movedTo returns the purchase moved in time so that it happens at t. Its
// account events move by the same amount, so they stay before it.
func (p Purchase) movedTo(t time.Time) Purchase {
	shift := t.Sub(p.Request.EventTime)
	p.Request.EventTime = t
	events := make([]AccountEvent, len(p.AccountEvents))
	for i, e := range p.AccountEvents {
		e.EventTime = e.EventTime.Add(shift)
		events[i] = e
	}
	p.AccountEvents = events
	return p
}

func copyOf[T any](v *T) *T {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
