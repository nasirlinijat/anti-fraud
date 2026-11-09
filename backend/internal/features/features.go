package features

import (
	"math"
	"strconv"
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
	"hour_of_day",
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
}

var (
	productCodes = map[string]float64{"W": 0, "H": 1, "C": 2, "S": 3, "R": 4}
	cardNetworks = map[string]float64{"visa": 0, "mastercard": 1, "american express": 2, "discover": 3}
	cardTypes    = map[string]float64{"debit": 0, "credit": 1, "debit or credit": 2, "charge card": 3}
	deviceTypes  = map[string]float64{"desktop": 0, "mobile": 1}
)

// NoEmail is the email domain key of a purchase without one.
const NoEmail = "none"

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
}

// EmailKey returns the purchaser email domain, or NoEmail when it is missing.
func (in Input) EmailKey() string {
	if in.EmailDomain == nil {
		return NoEmail
	}
	return *in.EmailDomain
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
}

// CardHistory summarizes the card's earlier purchases.
type CardHistory struct {
	Uses1h, Uses24h int64
	Players         int64
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

// History is everything the features need from earlier purchases.
type History struct {
	Player PlayerHistory
	Card   CardHistory
	Issuer IssuerHistory
	All    Count // all purchases
	Email  Count // purchases with this email domain
	Device Count // purchases with this device name; zero when it is missing
}

// Vector returns the feature values in the order of Names.
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
		float64(t.UTC().Hour()),
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
