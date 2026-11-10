package simulator

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"time"
)

// Pattern is the kind of traffic a purchase belongs to. It is known only to the
// simulator and is never sent to the score API.
type Pattern string

const (
	// PatternNormal is a regular player buying with their own card from their
	// own country.
	PatternNormal Pattern = "normal"
	// PatternCardTesting is a stolen card checked with a burst of small
	// purchases from a new account in another country.
	PatternCardTesting Pattern = "card_testing"
	// PatternAccountTakeover is an existing account used from another country
	// and a new device for a large purchase.
	PatternAccountTakeover Pattern = "account_takeover"
	// PatternNewAccountSpend is a large purchase from an account created a few
	// hours earlier.
	PatternNewAccountSpend Pattern = "new_account_spend"
)

// Patterns lists every pattern in a fixed order.
var Patterns = []Pattern{PatternNormal, PatternCardTesting, PatternAccountTakeover, PatternNewAccountSpend}

// Request is a score request in the API format.
type Request struct {
	TransactionID        string     `json:"transaction_id"`
	EventTime            time.Time  `json:"event_time"`
	AmountMinor          int64      `json:"amount_minor"`
	PlayerID             string     `json:"player_id"`
	CardToken            string     `json:"card_token"`
	CardIssuer           *string    `json:"card_issuer,omitempty"`
	CardNetwork          *string    `json:"card_network,omitempty"`
	CardType             *string    `json:"card_type,omitempty"`
	CardCountry          *string    `json:"card_country,omitempty"`
	CardCategory         *string    `json:"card_category,omitempty"`
	ProductCode          *string    `json:"product_code,omitempty"`
	BillingRegion        *string    `json:"billing_region,omitempty"`
	BillingCountry       *string    `json:"billing_country,omitempty"`
	BillingDistance      *float64   `json:"billing_distance,omitempty"`
	EmailDomain          *string    `json:"email_domain,omitempty"`
	RecipientEmailDomain *string    `json:"recipient_email_domain,omitempty"`
	DeviceType           *string    `json:"device_type,omitempty"`
	DeviceName           *string    `json:"device_name,omitempty"`
	DeviceID             *string    `json:"device_id,omitempty"`
	IPAddress            *string    `json:"ip_address,omitempty"`
	IPCountry            *string    `json:"ip_country,omitempty"`
	AccountCreatedAt     *time.Time `json:"account_created_at,omitempty"`
}

// Purchase is one generated request and the pattern it belongs to.
type Purchase struct {
	Pattern Pattern
	Request Request
}

// Config controls the generated traffic. The same Config always gives the same
// purchases.
type Config struct {
	Seed uint64
	// Run prefixes every transaction, player, card and device ID, so that
	// several runs can be sent to the same database.
	Run string
	// Start is the event time of the first purchase.
	Start time.Time
	// SuspiciousShare is the share of traffic, from 0 to 1, that starts a
	// suspicious pattern instead of a normal purchase.
	SuspiciousShare float64
	// Players is the number of regular players.
	Players int
	// MeanGap is the mean simulated time between two traffic events.
	MeanGap time.Duration
}

// DefaultConfig returns the settings used by the simulator command.
func DefaultConfig() Config {
	return Config{
		Seed:            1,
		Run:             "sim",
		Start:           time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC),
		SuspiciousShare: 0.1,
		Players:         300,
		MeanGap:         20 * time.Second,
	}
}

// Validate reports the first invalid setting.
func (c Config) Validate() error {
	switch {
	case c.Run == "":
		return fmt.Errorf("run must not be empty")
	case c.Start.IsZero():
		return fmt.Errorf("start must be set")
	case c.SuspiciousShare < 0 || c.SuspiciousShare > 1:
		return fmt.Errorf("suspicious share must be between 0 and 1, got %v", c.SuspiciousShare)
	case c.Players < 1:
		return fmt.Errorf("players must be at least 1, got %d", c.Players)
	case c.MeanGap <= 0:
		return fmt.Errorf("mean gap must be positive, got %v", c.MeanGap)
	}
	return nil
}

var (
	countries     = []string{"AZ", "TR", "GE", "KZ", "DE", "GB", "US", "UA"}
	cardNetworks  = []string{"visa", "visa", "visa", "mastercard", "mastercard", "american express", "discover"}
	cardTypes     = []string{"debit", "debit", "debit", "credit"}
	cardCategory  = []string{"226", "224", "166", "102", "117"}
	productCodes  = []string{"W", "W", "W", "C", "H", "R", "S"}
	emailDomains  = []string{"gmail.com", "gmail.com", "gmail.com", "yahoo.com", "outlook.com", "mail.ru", "icloud.com", "hotmail.com"}
	mobileDevices = []string{"SM-G991B", "SM-A515F", "iOS Device", "Redmi Note 8 Pro", "Pixel 7"}
	desktops      = []string{"Windows", "MacOS", "Linux"}
)

// Card velocity at which the default ruleset fires. Normal players stay below it.
const normalCardUsesPerHour = 3

type player struct {
	id         string
	card       card
	email      string
	country    string
	region     string
	deviceType string
	deviceName string
	deviceID   string
	ipAddress  string
	createdAt  time.Time
}

type card struct {
	token    string
	issuer   string
	network  string
	cardType string
	country  string
	category string
}

type generator struct {
	cfg     Config
	rng     *rand.Rand
	players []player
	recent  map[string][]time.Time
	next    int
	out     []Purchase
}

// Generate returns count purchases ordered by event time, then transaction ID.
func Generate(cfg Config, count int) ([]Purchase, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, fmt.Errorf("count must not be negative, got %d", count)
	}

	g := &generator{
		cfg:    cfg,
		rng:    rand.New(rand.NewPCG(cfg.Seed, 0x5eed)),
		recent: map[string][]time.Time{},
	}
	for range cfg.Players {
		g.players = append(g.players, g.newPlayer(cfg.Start.Add(-g.days(30, 900))))
	}

	// Every event is generated at or after the clock, and later events start
	// later, so the first count purchases by time are final once the clock
	// passes the last of them.
	clock := cfg.Start
	for len(g.out) < count || g.sortedPrefixEndsBefore(count, clock) {
		g.event(clock)
		clock = clock.Add(g.gap())
	}

	g.sort()
	return g.out[:count], nil
}

// StartEndingAt returns the start that makes the last of count purchases
// happen at end. Event times only depend on the start through their distance
// from it, so the purchases are the same as from any other start, shifted.
func StartEndingAt(cfg Config, count int, end time.Time) (time.Time, error) {
	cfg.Start = end
	purchases, err := Generate(cfg, count)
	if err != nil || len(purchases) == 0 {
		return end, err
	}
	return end.Add(-purchases[len(purchases)-1].Request.EventTime.Sub(end)), nil
}

func (g *generator) sortedPrefixEndsBefore(count int, clock time.Time) bool {
	g.sort()
	return count > 0 && !g.out[count-1].Request.EventTime.Before(clock)
}

func (g *generator) sort() {
	slices.SortStableFunc(g.out, func(a, b Purchase) int {
		if c := a.Request.EventTime.Compare(b.Request.EventTime); c != 0 {
			return c
		}
		return compareIDs(a.Request.TransactionID, b.Request.TransactionID)
	})
}

func compareIDs(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (g *generator) event(at time.Time) {
	if g.rng.Float64() >= g.cfg.SuspiciousShare {
		g.normal(at)
		return
	}
	switch g.rng.IntN(3) {
	case 0:
		g.cardTesting(at)
	case 1:
		g.accountTakeover(at)
	default:
		g.newAccountSpend(at)
	}
}

// normal buys with a regular player whose card is below the velocity limit.
func (g *generator) normal(at time.Time) {
	p := g.players[g.rng.IntN(len(g.players))]
	for range len(g.players) {
		if g.usesLastHour(p.card.token, at) < normalCardUsesPerHour {
			break
		}
		p = g.players[g.rng.IntN(len(g.players))]
	}
	if g.usesLastHour(p.card.token, at) >= normalCardUsesPerHour {
		return
	}
	amount := g.amount(199, 9999)
	if g.rng.Float64() < 0.03 {
		amount = g.amount(50000, 80000)
	}
	g.add(PatternNormal, g.request(p, at, amount))
}

// cardTesting sends 7 to 9 small purchases on one new card a minute or two
// apart. The first five pass the velocity rule; the rest exceed it.
func (g *generator) cardTesting(at time.Time) {
	p := g.newPlayer(at.Add(-g.minutes(30, 360)))
	ipCountry := g.otherCountry(p.card.country)
	p.ipAddress = g.ipAddress()
	burst := 7 + g.rng.IntN(3)
	for range burst {
		req := g.request(p, at, g.amount(100, 500))
		req.IPCountry = &ipCountry
		g.add(PatternCardTesting, req)
		at = at.Add(g.seconds(20, 120))
	}
}

// accountTakeover uses a regular player's account and card from another
// country and a new device for a large purchase.
func (g *generator) accountTakeover(at time.Time) {
	p := g.players[g.rng.IntN(len(g.players))]
	ipCountry := g.otherCountry(p.card.country)
	p.deviceType, p.deviceName = g.device()
	p.deviceID = g.id("device")
	p.ipAddress = g.ipAddress()
	req := g.request(p, at, g.amount(60000, 150000))
	req.IPCountry = &ipCountry
	g.add(PatternAccountTakeover, req)
}

// newAccountSpend makes a large purchase from an account created a few hours
// earlier, from the card's own country.
func (g *generator) newAccountSpend(at time.Time) {
	p := g.newPlayer(at.Add(-g.minutes(10, 720)))
	g.add(PatternNewAccountSpend, g.request(p, at, g.amount(50000, 120000)))
}

func (g *generator) add(pattern Pattern, req Request) {
	g.recent[req.CardToken] = append(g.recent[req.CardToken], req.EventTime)
	g.out = append(g.out, Purchase{Pattern: pattern, Request: req})
}

func (g *generator) usesLastHour(cardToken string, at time.Time) int {
	n := 0
	for _, t := range g.recent[cardToken] {
		if !t.After(at) && at.Sub(t) < time.Hour {
			n++
		}
	}
	return n
}

func (g *generator) newPlayer(createdAt time.Time) player {
	country := g.pick(countries)
	deviceType, deviceName := g.device()
	return player{
		id: g.id("player"),
		card: card{
			token:    g.id("card"),
			issuer:   fmt.Sprint(1000 + g.rng.IntN(17000)),
			network:  g.pick(cardNetworks),
			cardType: g.pick(cardTypes),
			country:  country,
			category: g.pick(cardCategory),
		},
		email:      g.pick(emailDomains),
		country:    country,
		region:     fmt.Sprint(100 + g.rng.IntN(440)),
		deviceType: deviceType,
		deviceName: deviceName,
		deviceID:   g.id("device"),
		ipAddress:  g.ipAddress(),
		createdAt:  createdAt.UTC(),
	}
}

func (g *generator) request(p player, at time.Time, amount int64) Request {
	created := p.createdAt
	req := Request{
		TransactionID:    g.id("txn"),
		EventTime:        at.UTC(),
		AmountMinor:      amount,
		PlayerID:         p.id,
		CardToken:        p.card.token,
		CardIssuer:       ptr(p.card.issuer),
		CardNetwork:      ptr(p.card.network),
		CardType:         ptr(p.card.cardType),
		CardCountry:      ptr(p.card.country),
		CardCategory:     ptr(p.card.category),
		ProductCode:      ptr(g.pick(productCodes)),
		BillingRegion:    ptr(p.region),
		BillingCountry:   ptr("87"),
		EmailDomain:      ptr(p.email),
		DeviceType:       ptr(p.deviceType),
		DeviceName:       ptr(p.deviceName),
		DeviceID:         ptr(p.deviceID),
		IPAddress:        ptr(p.ipAddress),
		IPCountry:        ptr(p.country),
		AccountCreatedAt: &created,
	}
	if g.rng.Float64() < 0.4 {
		req.BillingDistance = ptr(math.Round(g.rng.Float64()*500) / 10)
	}
	if g.rng.Float64() < 0.15 {
		req.RecipientEmailDomain = ptr(g.pick(emailDomains))
	}
	return req
}

func (g *generator) id(kind string) string {
	g.next++
	return fmt.Sprintf("%s-%s-%d", g.cfg.Run, kind, g.next)
}

func (g *generator) device() (string, string) {
	if g.rng.Float64() < 0.6 {
		return "mobile", g.pick(mobileDevices)
	}
	return "desktop", g.pick(desktops)
}

func (g *generator) ipAddress() string {
	return fmt.Sprintf("198.51.%d.%d", g.rng.IntN(256), 1+g.rng.IntN(254))
}

func (g *generator) otherCountry(country string) string {
	for {
		if c := g.pick(countries); c != country {
			return c
		}
	}
}

func (g *generator) pick(values []string) string {
	return values[g.rng.IntN(len(values))]
}

func (g *generator) amount(low, high int64) int64 {
	return low + g.rng.Int64N(high-low+1)
}

// gap draws the time to the next traffic event, rounded to whole seconds.
func (g *generator) gap() time.Duration {
	seconds := math.Round(g.rng.ExpFloat64() * g.cfg.MeanGap.Seconds())
	return time.Duration(max(seconds, 1)) * time.Second
}

func (g *generator) seconds(low, high int) time.Duration {
	return time.Duration(low+g.rng.IntN(high-low+1)) * time.Second
}

func (g *generator) minutes(low, high int) time.Duration {
	return time.Duration(low+g.rng.IntN(high-low+1)) * time.Minute
}

func (g *generator) days(low, high int) time.Duration {
	return time.Duration(low+g.rng.IntN(high-low+1)) * 24 * time.Hour
}

func ptr[T any](v T) *T { return &v }
