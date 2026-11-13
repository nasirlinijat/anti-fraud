package simulator

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// accountEventStream seeds the random stream of account events. It is
// separate from the purchase stream, so the account events never change the
// purchases of a seed.
const accountEventStream = 0xacc0

// Account event types, as POST /account-events takes them.
const (
	accountLogin         = "login"
	accountPasswordReset = "password_reset"
)

// How players use a game account. Every account, honest or not, has logins:
// the game asks for one before it sells anything.
const (
	// accountHistoryDays is how far back the account system reports the
	// earlier logins of an established account when it first buys in a run.
	accountHistoryDays = 30
	// minLoginDayShare and maxLoginDayShare bound the share of days on which
	// an established player logs in. Each player draws their own share.
	minLoginDayShare, maxLoginDayShare = 0.05, 0.4
	// freshAccountAge is the age below which an account has no earlier
	// logins, only the one right after it was created.
	freshAccountAge = 24 * time.Hour
	// sessionGap is the time without a login from the same device and
	// country after which a purchase starts a new session with a login.
	sessionGap = 6 * time.Hour
	// secondDeviceShare is the share of players who also play on a second
	// device, such as a tablet or a computer at home.
	secondDeviceShare = 0.2
	// secondDeviceLoginShare is the share of such a player's earlier logins
	// made on the second device.
	secondDeviceLoginShare = 0.3
	// secondDeviceSessionShare is the share of such a player's sessions that
	// follow a login on the second device 1 to 20 hours earlier.
	secondDeviceSessionShare = 0.15
	// regularResetShare is the share of established accounts that reset
	// their password in a resetPeriod, before one of their logins.
	regularResetShare = 0.02
	resetPeriod       = 30 * 24 * time.Hour
)

// span is a range of durations, both ends included.
type span struct{ low, high time.Duration }

// accountProfile is how the accounts of a pattern log in before a purchase.
type accountProfile struct {
	// loginBefore is how long before the purchase the session login is.
	loginBefore span
	// everyPurchase logs in before every purchase, as scripts do, instead of
	// once per session.
	everyPurchase bool
	// extraLogins is the most further logins from the same device within
	// extraWithin before the session login.
	extraLogins int
	extraWithin time.Duration
	// resetShare is the share of session logins that follow a password reset
	// 2 to 60 minutes earlier, beyond the regular resets of every account.
	resetShare float64
	// away is a player back after a long break, without logins in the last
	// accountHistoryDays.
	away bool
}

// regularAccount is the profile of every pattern without its own.
var regularAccount = accountProfile{loginBefore: span{time.Minute, time.Hour}}

// accountProfiles override regularAccount. A new phone or a shared computer
// needs a login and sometimes a forgotten password, and a player back after
// months often resets it. Takeovers log in from the attacker's device, often
// after resetting the password, and sometimes several times. Card testing and
// device farm scripts log in before every purchase. A fraudster's own aged
// account (stolen cards, slow card testing, low and slow fraud) logs in like a
// regular player's, and a new account buying a lot looks the same whether or
// not it is fraud.
var accountProfiles = map[Pattern]accountProfile{
	PatternNewPhone:        {loginBefore: span{time.Minute, 30 * time.Minute}, resetShare: 0.10},
	PatternInternetCafe:    {loginBefore: span{time.Minute, 20 * time.Minute}, resetShare: 0.05},
	PatternReturningPlayer: {loginBefore: span{time.Minute, 30 * time.Minute}, resetShare: 0.25, away: true},
	PatternNewCasualPlayer: {loginBefore: span{time.Minute, 30 * time.Minute}},
	PatternNewBigSpender:   {loginBefore: span{time.Minute, 30 * time.Minute}},
	PatternNewAccountSpend: {loginBefore: span{time.Minute, 30 * time.Minute}},
	PatternAccountTakeover: {loginBefore: span{time.Minute, 15 * time.Minute}, resetShare: 0.45, extraLogins: 2, extraWithin: time.Hour},
	PatternQuietTakeover:   {loginBefore: span{time.Minute, 15 * time.Minute}, resetShare: 0.30, extraLogins: 1, extraWithin: time.Hour},
	PatternCardTesting:     {loginBefore: span{10 * time.Second, 2 * time.Minute}, everyPurchase: true, extraLogins: 2, extraWithin: 5 * time.Minute},
	PatternDeviceFarm:      {loginBefore: span{10 * time.Second, 5 * time.Minute}, everyPurchase: true, extraLogins: 1, extraWithin: 5 * time.Minute},
}

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

// account is what the account system knows about a player.
type account struct {
	createdAt  time.Time
	homeDevice string
	// homeCountry is the regular players' own country. Other players take it
	// from their first purchase, because a scenario can place them elsewhere.
	homeCountry string
	regular     bool
	seen        bool
	// lastLogin is the latest login per device and country.
	lastLogin map[string]time.Time
	// resets holds the reset periods in which the player reset the password.
	resets map[int]bool
}

// registerAccount records a new player's account. A scenario player's
// account is forgotten after the traffic event that created it.
func (g *generator) registerAccount(p player) {
	g.accounts[p.id] = &account{createdAt: p.createdAt, homeDevice: p.deviceID, homeCountry: p.country}
	g.eventAccounts = append(g.eventAccounts, p.id)
}

// keepAccounts marks the accounts registered so far as regular players, who
// stay for the whole run.
func (g *generator) keepAccounts() {
	for _, id := range g.eventAccounts {
		g.accounts[id].regular = true
	}
	g.eventAccounts = nil
}

// forgetEventAccounts drops the accounts of the scenario players created by
// the last traffic event. Their purchases are all added by then.
func (g *generator) forgetEventAccounts() {
	for _, id := range g.eventAccounts {
		delete(g.accounts, id)
	}
	g.eventAccounts = g.eventAccounts[:0]
}

// accountEvents returns the account events the game reports before the
// purchase, oldest first: the account's earlier logins when it first buys,
// then the logins and resets of the purchase's session. They use only the
// account event stream and seeded draws, and leave out the device and
// country when the player's client leaves them out of the purchase.
func (g *generator) accountEvents(pattern Pattern, req Request) []AccountEvent {
	acc := g.accountOf(req)
	profile, special := accountProfiles[pattern]
	if !special {
		profile = regularAccount
	}
	var events []AccountEvent
	if !acc.seen {
		acc.seen = true
		events = g.earlierEvents(acc, req, profile)
	}
	events = append(events, g.sessionEvents(acc, req, profile)...)
	slices.SortStableFunc(events, func(a, b AccountEvent) int { return a.EventTime.Compare(b.EventTime) })
	for i := range events {
		events[i].EventID = fmt.Sprintf("%s-%s-%d", req.TransactionID, events[i].EventType, i+1)
	}
	return events
}

// accountOf returns the purchase's account, whose home country a scenario
// player takes from the first purchase.
func (g *generator) accountOf(req Request) *account {
	acc := g.accounts[req.PlayerID]
	if !acc.seen && !acc.regular && req.IPCountry != nil {
		acc.homeCountry = *req.IPCountry
	}
	return acc
}

// earlierEvents returns the logins of the account before the run reached
// it: the login right after a fresh account was created, or an established
// player's logins on some of the 30 days before the day of the purchase,
// sometimes after a password reset. A player back from a long break has none.
func (g *generator) earlierEvents(acc *account, req Request, profile accountProfile) []AccountEvent {
	age := req.EventTime.Sub(acc.createdAt)
	if age < freshAccountAge {
		return g.registrationLogin(acc, req)
	}
	if profile.away {
		return nil
	}
	share := minLoginDayShare + (maxLoginDayShare-minLoginDayShare)*g.seededDraw(req.PlayerID, "login_days")
	var events []AccountEvent
	for day := 1; day <= accountHistoryDays; day++ {
		if g.accountRng.Float64() >= share {
			continue
		}
		at := req.EventTime.Add(-time.Duration(day)*24*time.Hour - g.accountDuration(span{0, 24*time.Hour - time.Second}))
		if at.After(acc.createdAt) {
			events = append(events, g.accountEvent(req, accountLogin, at, g.earlierDevice(acc, req), g.homeCountry(acc, req)))
		}
	}
	if len(events) > 0 && g.accountRng.Float64() < regularResetShare {
		login := events[g.accountRng.IntN(len(events))]
		events = append(events, g.resetBefore(req, login))
	}
	return events
}

// registrationLogin is the login right after a fresh account was created,
// from the device and country of its first purchase.
func (g *generator) registrationLogin(acc *account, req Request) []AccountEvent {
	at := acc.createdAt.Add(g.accountDuration(span{0, 2 * time.Minute}))
	if !at.Before(req.EventTime) {
		return nil
	}
	return []AccountEvent{g.accountEvent(req, accountLogin, at, req.DeviceID, req.IPCountry)}
}

// sessionEvents returns the login before a purchase that starts a session,
// or before every purchase of a script, with the further logins and the
// password reset the profile adds.
func (g *generator) sessionEvents(acc *account, req Request, profile accountProfile) []AccountEvent {
	key := sessionKey(req)
	last, loggedIn := acc.lastLogin[key]
	if loggedIn && req.EventTime.Sub(last) < sessionGap && !profile.everyPurchase {
		return nil
	}
	at := req.EventTime.Add(-g.accountDuration(profile.loginBefore))
	if !at.After(acc.createdAt) {
		return nil
	}
	login := g.accountEvent(req, accountLogin, at, req.DeviceID, req.IPCountry)
	events := []AccountEvent{login}
	for range g.accountRng.IntN(profile.extraLogins + 1) {
		extra := at.Add(-g.accountDuration(span{time.Second, profile.extraWithin}))
		if extra.After(acc.createdAt) {
			events = append(events, g.accountEvent(req, accountLogin, extra, req.DeviceID, req.IPCountry))
		}
	}
	if g.secondDeviceSession(acc, req) {
		earlier := at.Add(-g.accountDuration(span{time.Hour, 20 * time.Hour}))
		events = append(events, g.accountEvent(req, accountLogin, earlier, secondDevice(req), g.homeCountry(acc, req)))
	}
	if g.accountRng.Float64() < profile.resetShare || g.regularReset(acc, req, at) {
		events = append(events, g.resetBefore(req, login))
	}
	if acc.lastLogin == nil {
		acc.lastLogin = map[string]time.Time{}
	}
	acc.lastLogin[key] = at
	return events
}

// secondDeviceSession reports whether a session on the player's own device
// follows a login on their second device earlier that day.
func (g *generator) secondDeviceSession(acc *account, req Request) bool {
	onHomeDevice := req.DeviceID != nil && *req.DeviceID == acc.homeDevice
	return onHomeDevice && g.hasSecondDevice(req.PlayerID) && g.accountRng.Float64() < secondDeviceSessionShare
}

// regularReset reports whether the login is the first of the account after
// its regular password reset of the period. Each established account resets
// in regularResetShare of the periods, at a time drawn from the seed.
func (g *generator) regularReset(acc *account, req Request, login time.Time) bool {
	if login.Sub(acc.createdAt) < freshAccountAge {
		return false
	}
	period := int(math.Floor(float64(login.Sub(g.cfg.Start)) / float64(resetPeriod)))
	key := fmt.Sprintf("password_reset_%d", period)
	if acc.resets[period] || g.seededDraw(req.PlayerID, key) >= regularResetShare {
		return false
	}
	due := g.cfg.Start.Add(time.Duration(period) * resetPeriod).
		Add(time.Duration(g.seededDraw(req.PlayerID, key+"_at") * float64(resetPeriod)))
	if login.Before(due) {
		return false
	}
	if acc.resets == nil {
		acc.resets = map[int]bool{}
	}
	acc.resets[period] = true
	return true
}

// resetBefore returns a password reset 2 to 60 minutes before the login, on
// the login's device and country.
func (g *generator) resetBefore(req Request, login AccountEvent) AccountEvent {
	at := login.EventTime.Add(-g.accountDuration(span{2 * time.Minute, time.Hour}))
	return g.accountEvent(req, accountPasswordReset, at, login.DeviceID, login.IPCountry)
}

// earlierDevice is the device of an earlier login: the player's own device,
// or sometimes their second one.
func (g *generator) earlierDevice(acc *account, req Request) *string {
	if req.DeviceID == nil {
		return nil
	}
	if g.hasSecondDevice(req.PlayerID) && g.accountRng.Float64() < secondDeviceLoginShare {
		return secondDevice(req)
	}
	return ptr(acc.homeDevice)
}

// homeCountry is the country of the player's logins at home, missing when
// the client leaves the country out.
func (g *generator) homeCountry(acc *account, req Request) *string {
	if req.IPCountry == nil {
		return nil
	}
	return ptr(acc.homeCountry)
}

func (g *generator) hasSecondDevice(playerID string) bool {
	return g.seededDraw(playerID, "second_device") < secondDeviceShare
}

// secondDevice is the ID of the player's second device, derived from the
// player ID so that it never takes an ID from the shared counter.
func secondDevice(req Request) *string {
	if req.DeviceID == nil {
		return nil
	}
	return ptr(req.PlayerID + "-second-device")
}

// sessionKey identifies a session by device and country.
func sessionKey(req Request) string {
	device, country := "", ""
	if req.DeviceID != nil {
		device = *req.DeviceID
	}
	if req.IPCountry != nil {
		country = *req.IPCountry
	}
	return device + "|" + country
}

// accountEvent returns an event of the request's player. Its ID is set once
// the purchase's events are sorted.
func (g *generator) accountEvent(req Request, eventType string, at time.Time, device, country *string) AccountEvent {
	return AccountEvent{
		PlayerID:  req.PlayerID,
		EventType: eventType,
		EventTime: at,
		DeviceID:  copyOf(device),
		IPCountry: copyOf(country),
	}
}

// accountDuration draws a duration in the span, in whole seconds.
func (g *generator) accountDuration(s span) time.Duration {
	seconds := int64(s.low / time.Second)
	return time.Duration(seconds+g.accountRng.Int64N(int64(s.high/time.Second)-seconds+1)) * time.Second
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
