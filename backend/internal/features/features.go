package features

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Missing is the value of a feature that is unknown or has no history yet.
const Missing = -1.0

// Names lists the model features in the order the model expects. It matches
// FEATURES in ml/features.py and the table in ml/FEATURES.md.
var Names = []string{
	"amount",
	"cents",
	"product_code",
	"card_network",
	"card_type",
	"player_txn_count",
	"player_avg_amount",
	"amount_to_avg",
	"secs_since_last",
	"secs_since_first",
	"card_uses_1h",
	"card_uses_24h",
	"player_distinct_cards",
	"email_is_new_for_player",
	"card_country",
	"card_category",
	"billing_country",
	"email_domain_share",
	"has_recipient_email",
	"recipient_email_differs",
	"player_max_amount",
	"player_uses_1h",
	"player_uses_24h",
	"player_same_amount_count",
	"player_distinct_emails",
	"email_players_share",
	"has_device_info",
	"device_type",
	"device_info_share",
	"device_players_share",
	"device_is_new_for_player",
	"player_distinct_devices",
	"card_distinct_players",
	"card1_distinct_players",
	"card1_distinct_addresses",
	"card1_distinct_emails",
	"billing_distance",
	"account_age_days",
	"has_client_info",
	"os_family",
	"browser_family",
	"os_share",
	"browser_share",
	"screen_share",
	"screen_area",
	"os_is_new_for_player",
	"browser_is_new_for_player",
	"player_distinct_browsers",
	"card_issuer_code",
	"billing_region_code",
	"ip_country_differs_from_card",
	"device_id_is_new_for_player",
	"device_distinct_players",
	"billing_region_is_new_for_card",
	"cvv_result_code",
	"avs_result_code",
	"three_ds_result_code",
	"ip_is_hosting",
	"ip_is_anonymous",
	"hours_since_password_reset",
	"device_first_login_hours",
	"country_first_login_hours",
	"login_devices_24h",
}

var (
	productCodes = map[string]float64{"W": 0, "H": 1, "C": 2, "S": 3, "R": 4}
	cardNetworks = map[string]float64{"visa": 0, "mastercard": 1, "american express": 2, "discover": 3}
	cardTypes    = map[string]float64{"debit": 0, "credit": 1, "debit or credit": 2, "charge card": 3}
	deviceTypes  = map[string]float64{"desktop": 0, "mobile": 1}
	// Keyed by the first word of the value, in lower case.
	osFamilies      = map[string]float64{"windows": 0, "ios": 1, "mac": 2, "android": 3, "linux": 4}
	browserFamilies = map[string]float64{
		"chrome": 0, "mobile": 1, "safari": 2, "ie": 3, "edge": 4,
		"firefox": 5, "samsung": 6, "android": 7, "opera": 8,
	}
	// The payment verification codes go from a pass (0) to a failure (the
	// highest code), with a skipped check in between.
	cvvResults     = map[string]float64{"match": 0, "not_checked": 1, "mismatch": 2}
	avsResults     = map[string]float64{"match": 0, "partial": 1, "not_checked": 2, "mismatch": 3}
	threeDSResults = map[string]float64{"authenticated": 0, "attempted": 1, "not_enrolled": 2, "failed": 3}
	screenSize     = regexp.MustCompile(`^(\d+)x(\d+)$`)
)

// Account events count only within these windows before the purchase, so a
// player's whole account history is never read.
const (
	AccountLookback    = 720 * time.Hour
	LoginDevicesWindow = 24 * time.Hour
)

// NoEmail is the email domain key of a purchase without one.
const NoEmail = "none"

// NoRegion is the billing region key of a purchase without one.
const NoRegion = "none"

// Input is the part of a score request the features are computed from.
type Input struct {
	TransactionID        string
	EventTime            time.Time
	AmountMinor          int64
	PlayerID             string
	CardToken            string
	CardIssuer           *string
	CardNetwork          *string
	CardType             *string
	CardCountry          *string
	CardCategory         *string
	ProductCode          *string
	BillingRegion        *string
	BillingCountry       *string
	BillingDistance      *float64
	EmailDomain          *string
	RecipientEmailDomain *string
	DeviceType           *string
	DeviceName           *string
	DeviceID             *string
	IPCountry            *string
	AccountCreatedAt     *time.Time
	ClientOS             *string
	ClientBrowser        *string
	ScreenResolution     *string
	CVVResult            *string
	AVSResult            *string
	ThreeDSResult        *string
	// IPNetwork is what IP intelligence reported about the purchase's IP
	// address. It is nil when the request has no address, the lookup is off,
	// or it failed, so the model sees one missing value for all three.
	IPNetwork *IPNetwork
}

// IPNetwork is the kind of network an IP address belongs to.
type IPNetwork struct {
	Hosting bool
	// Anonymous is true for a VPN, a public proxy, or a Tor exit.
	Anonymous bool
}

// EmailKey returns the purchaser email domain, or NoEmail when it is missing.
func (in Input) EmailKey() string {
	if in.EmailDomain == nil {
		return NoEmail
	}
	return *in.EmailDomain
}

// RegionKey returns the billing region, or NoRegion when it is missing.
func (in Input) RegionKey() string {
	if in.BillingRegion == nil {
		return NoRegion
	}
	return *in.BillingRegion
}

// AccountCountry returns the purchase's IP country as account events store
// it: upper case. Only ASCII letters change, so ml/features.py can match it
// exactly.
func (in Input) AccountCountry() *string {
	if in.IPCountry == nil {
		return nil
	}
	upper := strings.Map(func(r rune) rune {
		if 'a' <= r && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}, *in.IPCountry)
	return &upper
}

// PlayerHistory summarizes the player's earlier purchases.
type PlayerHistory struct {
	Purchases        int64
	AmountTotalMinor int64
	AmountMaxMinor   int64
	First, Last      time.Time
	Uses1h, Uses24h  int64
	SameAmount       int64
	DistinctCards    int64
	DistinctEmails   int64
	EmailUses        int64 // earlier purchases with this email domain
	DistinctDevices  int64 // known device names only
	DeviceUses       int64 // earlier purchases with this device name
	DeviceIDUses     int64 // earlier purchases with this device ID
	OSUses           int64 // earlier purchases with this client OS
	BrowserUses      int64 // earlier purchases with this client browser
	ScreenUses       int64 // earlier purchases with this screen resolution
	DistinctBrowsers int64 // known client browsers only
}

// CardHistory summarizes the card's earlier purchases.
type CardHistory struct {
	Purchases       int64
	Uses1h, Uses24h int64
	Players         int64
	RegionUses      int64 // earlier purchases with this billing region
}

// IssuerHistory summarizes the card issuer's earlier purchases.
type IssuerHistory struct {
	Players        int64
	BillingRegions int64
	EmailDomains   int64
}

// Count is a running total of purchases and distinct players.
type Count struct {
	Purchases int64
	Players   int64
}

// History is everything the features need from earlier purchases and
// account events.
type History struct {
	Player  PlayerHistory
	Card    CardHistory
	Issuer  IssuerHistory
	All     Count // all purchases
	Email   Count // purchases with this email domain
	Device  Count // purchases with this device name; zero when it is missing
	OS      Count // purchases with this client OS; zero when it is missing
	Browser Count // purchases with this client browser; zero when it is missing
	Screen  Count // purchases with this screen resolution; zero when it is missing
	// DeviceIDPlayers counts the earlier players on this device ID; zero when
	// it is missing.
	DeviceIDPlayers int64
	Account         AccountActivity
}

// AccountActivity summarizes the player's account events in the
// AccountLookback before the purchase. Events at or after the purchase time
// are never read.
type AccountActivity struct {
	// HasEvents is true when the player has any account event in the window.
	HasEvents         bool
	LastPasswordReset *time.Time
	// DeviceFirstLogin is the first login in the window from the purchase's
	// device ID; nil without a device ID or such a login.
	DeviceFirstLogin *time.Time
	// CountryFirstLogin is the first login in the window from the purchase's
	// IP country, compared in upper case; nil without one.
	CountryFirstLogin *time.Time
	// LoginDevices24h counts the distinct device IDs of the player's logins
	// in the LoginDevicesWindow before the purchase.
	LoginDevices24h int64
}

// Vector returns the feature values in the order of Names. It is long because
// it is one flat list with a line per feature.
func Vector(in Input, h History) []float64 {
	p := h.Player
	amount := float64(in.AmountMinor) / 100
	hasHistory := p.Purchases > 0
	knownDevice := in.DeviceName != nil
	t := in.EventTime

	avg := Missing
	if hasHistory {
		avg = float64(p.AmountTotalMinor) / 100 / float64(p.Purchases)
	}
	amountToAvg := Missing
	if avg > 0 {
		amountToAvg = amount / avg
	}

	return []float64{
		amount,
		float64(in.AmountMinor%100) / 100,
		code(productCodes, in.ProductCode),
		code(cardNetworks, in.CardNetwork),
		code(cardTypes, in.CardType),
		float64(p.Purchases),
		avg,
		amountToAvg,
		when(hasHistory, func() float64 { return t.Sub(p.Last).Seconds() }),
		when(hasHistory, func() float64 { return t.Sub(p.First).Seconds() }),
		float64(h.Card.Uses1h),
		float64(h.Card.Uses24h),
		float64(p.DistinctCards),
		flag(hasHistory && p.EmailUses == 0),
		number(in.CardCountry),
		number(in.CardCategory),
		number(in.BillingCountry),
		share(h.Email.Purchases, h.All.Purchases),
		flag(in.RecipientEmailDomain != nil),
		flag(in.RecipientEmailDomain != nil && (in.EmailDomain == nil || *in.RecipientEmailDomain != *in.EmailDomain)),
		when(hasHistory, func() float64 { return float64(p.AmountMaxMinor) / 100 }),
		float64(p.Uses1h),
		float64(p.Uses24h),
		float64(p.SameAmount),
		float64(p.DistinctEmails),
		share(h.Email.Players, h.All.Players),
		flag(in.DeviceType != nil || in.DeviceName != nil),
		code(deviceTypes, in.DeviceType),
		when(knownDevice, func() float64 { return share(h.Device.Purchases, h.All.Purchases) }),
		when(knownDevice, func() float64 { return share(h.Device.Players, h.All.Players) }),
		flag(knownDevice && hasHistory && p.DeviceUses == 0),
		float64(p.DistinctDevices),
		float64(h.Card.Players),
		float64(h.Issuer.Players),
		float64(h.Issuer.BillingRegions),
		float64(h.Issuer.EmailDomains),
		distance(in.BillingDistance),
		accountAge(in.AccountCreatedAt, t),
		flag(in.ClientOS != nil || in.ClientBrowser != nil || in.ScreenResolution != nil),
		family(osFamilies, in.ClientOS),
		family(browserFamilies, in.ClientBrowser),
		when(in.ClientOS != nil, func() float64 { return share(h.OS.Purchases, h.All.Purchases) }),
		when(in.ClientBrowser != nil, func() float64 { return share(h.Browser.Purchases, h.All.Purchases) }),
		when(in.ScreenResolution != nil, func() float64 { return share(h.Screen.Purchases, h.All.Purchases) }),
		screenArea(in.ScreenResolution),
		flag(in.ClientOS != nil && hasHistory && p.OSUses == 0),
		flag(in.ClientBrowser != nil && hasHistory && p.BrowserUses == 0),
		float64(p.DistinctBrowsers),
		number(in.CardIssuer),
		number(in.BillingRegion),
		countryDiffers(in.IPCountry, in.CardCountry),
		flag(in.DeviceID != nil && hasHistory && p.DeviceIDUses == 0),
		when(in.DeviceID != nil, func() float64 { return float64(h.DeviceIDPlayers) }),
		flag(h.Card.Purchases > 0 && h.Card.RegionUses == 0),
		code(cvvResults, in.CVVResult),
		code(avsResults, in.AVSResult),
		code(threeDSResults, in.ThreeDSResult),
		ipFlag(in.IPNetwork, func(n IPNetwork) bool { return n.Hosting }),
		ipFlag(in.IPNetwork, func(n IPNetwork) bool { return n.Anonymous }),
		hoursSince(h.Account.LastPasswordReset, t),
		hoursSince(h.Account.DeviceFirstLogin, t),
		hoursSince(h.Account.CountryFirstLogin, t),
		when(h.Account.HasEvents, func() float64 { return float64(h.Account.LoginDevices24h) }),
	}
}

// Map pairs each feature name with its value, for saving with a decision.
func Map(values []float64) map[string]float64 {
	m := make(map[string]float64, len(values))
	for i, v := range values {
		m[Names[i]] = v
	}
	return m
}

// countryDiffers compares the connection's country with the card's, as text.
func countryDiffers(ipCountry, cardCountry *string) float64 {
	if ipCountry == nil || cardCountry == nil {
		return Missing
	}
	return flag(*ipCountry != *cardCountry)
}

// ipFlag reads one kind of network from the IP intelligence result, or
// returns Missing without one.
func ipFlag(network *IPNetwork, is func(IPNetwork) bool) float64 {
	if network == nil {
		return Missing
	}
	return flag(is(*network))
}

// hoursSince returns the hours from an account event to the purchase, or
// Missing without the event.
func hoursSince(event *time.Time, at time.Time) float64 {
	if event == nil {
		return Missing
	}
	return at.Sub(*event).Hours()
}

// accountAge returns the whole days between account creation and the purchase.
func accountAge(created *time.Time, at time.Time) float64 {
	if created == nil {
		return Missing
	}
	return math.Floor(at.Sub(*created).Seconds() / 86400)
}

// family looks up the first word of value, in lower case.
func family(table map[string]float64, value *string) float64 {
	if value == nil {
		return Missing
	}
	word, _, _ := strings.Cut(strings.ToLower(*value), " ")
	return code(table, &word)
}

// screenArea reads a resolution written as WIDTHxHEIGHT.
func screenArea(value *string) float64 {
	if value == nil {
		return Missing
	}
	m := screenSize.FindStringSubmatch(*value)
	if m == nil {
		return Missing
	}
	width, err1 := strconv.ParseFloat(m[1], 64)
	height, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil {
		return Missing
	}
	return width * height
}

func code(table map[string]float64, value *string) float64 {
	if value == nil {
		return Missing
	}
	if v, ok := table[*value]; ok {
		return v
	}
	return Missing
}

// number reads a numeric code sent as text, such as card_country "150".
func number(value *string) float64 {
	if value == nil {
		return Missing
	}
	v, err := strconv.ParseFloat(*value, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return Missing
	}
	return v
}

func distance(value *float64) float64 {
	if value == nil {
		return Missing
	}
	return *value
}

func share(part, total int64) float64 {
	if total <= 0 {
		return Missing
	}
	return float64(part) / float64(total)
}

func flag(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func when(ok bool, value func() float64) float64 {
	if !ok {
		return Missing
	}
	return value()
}
