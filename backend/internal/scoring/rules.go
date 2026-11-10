package scoring

import (
	"fmt"
	"math/big"
	"time"
)

// RuleName identifies a fraud rule.
type RuleName string

const (
	RuleHighAmount      RuleName = "high_amount"
	RuleCardVelocity    RuleName = "card_velocity"
	RuleNewAccount      RuleName = "new_account"
	RuleCountryMismatch RuleName = "country_mismatch"
	RuleDeviceCardCount RuleName = "device_card_count"
	RuleNewDeviceSpike  RuleName = "new_device_spike"

	// RuleBlocklist names the entry of a purchase that matched the blocklist.
	// It is not a configurable rule, so Known does not report it.
	RuleBlocklist RuleName = "blocklist"
)

// rules lists the configurable rules in evaluation order. Each has a check and
// a message that states the value that made it fire and its limit.
var rules = []struct {
	name    RuleName
	check   func(Input, RuleConfig) bool
	message func(Input, RuleConfig) string
}{
	{RuleHighAmount, highAmount, highAmountMessage},
	{RuleCardVelocity, cardVelocity, cardVelocityMessage},
	{RuleNewAccount, newAccount, newAccountMessage},
	{RuleCountryMismatch, countryMismatch, countryMismatchMessage},
	{RuleDeviceCardCount, deviceCardCount, deviceCardCountMessage},
	{RuleNewDeviceSpike, newDeviceSpike, newDeviceSpikeMessage},
}

// Known reports whether the scoring core implements a rule with this name.
func (n RuleName) Known() bool {
	for _, r := range rules {
		if r.name == n {
			return true
		}
	}
	return false
}

func highAmount(in Input, cfg RuleConfig) bool {
	return in.AmountMinor >= cfg.Limit
}

func cardVelocity(in Input, cfg RuleConfig) bool {
	return int64(in.CardPurchasesLastHour) >= cfg.Limit
}

func deviceCardCount(in Input, cfg RuleConfig) bool {
	return in.DeviceCardsLastHour > 0 && in.DeviceCardsLastHour >= cfg.Limit
}

func newDeviceSpike(in Input, cfg RuleConfig) bool {
	history := in.TrustedHistory
	if history == nil || history.Count < cfg.Limit || history.DeviceSeen || in.AmountMinor < cfg.MinAmountMinor {
		return false
	}
	// Compare with the exact average without division or fixed-width overflow.
	var amountTimesCount, multipliedTotal big.Int
	amountTimesCount.Mul(big.NewInt(in.AmountMinor), big.NewInt(history.Count))
	multipliedTotal.Mul(history.TotalMinor, big.NewInt(int64(cfg.AmountMultiplier)))
	return amountTimesCount.Cmp(&multipliedTotal) >= 0
}

// newAccount takes limit in hours.
func newAccount(in Input, cfg RuleConfig) bool {
	if in.AccountCreatedAt == nil {
		return false
	}
	return in.EventTime.Sub(*in.AccountCreatedAt) < time.Duration(cfg.Limit)*time.Hour
}

func countryMismatch(in Input, _ RuleConfig) bool {
	if in.IPCountry == nil || in.CardCountry == "" {
		return false
	}
	return *in.IPCountry != in.CardCountry
}

func highAmountMessage(in Input, cfg RuleConfig) string {
	return fmt.Sprintf("Amount %d minor units is at or above the limit of %d", in.AmountMinor, cfg.Limit)
}

func cardVelocityMessage(in Input, cfg RuleConfig) string {
	return fmt.Sprintf("Card used %d %s in 60 minutes (limit %d)", in.CardPurchasesLastHour, plural(int64(in.CardPurchasesLastHour), "time", "times"), cfg.Limit)
}

func deviceCardCountMessage(in Input, cfg RuleConfig) string {
	return fmt.Sprintf("Device used %d distinct %s in 60 minutes (limit %d)", in.DeviceCardsLastHour, plural(in.DeviceCardsLastHour, "card", "cards"), cfg.Limit)
}

func newDeviceSpikeMessage(in Input, cfg RuleConfig) string {
	return fmt.Sprintf("Amount %d minor units on a new device is at least %d times the average of %d trusted %s in %d %s (minimum amount %d minor units, minimum history %d)",
		in.AmountMinor, cfg.AmountMultiplier, in.TrustedHistory.Count, plural(in.TrustedHistory.Count, "purchase", "purchases"),
		cfg.WindowDays, plural(int64(cfg.WindowDays), "day", "days"), cfg.MinAmountMinor, cfg.Limit)
}

func newAccountMessage(in Input, cfg RuleConfig) string {
	age := time.Duration(0)
	if in.AccountCreatedAt != nil {
		age = max(in.EventTime.Sub(*in.AccountCreatedAt), 0)
	}
	hours := int64(age / time.Hour)
	return fmt.Sprintf("Account is %d %s old, under the limit of %d %s", hours, plural(hours, "hour", "hours"), cfg.Limit, plural(cfg.Limit, "hour", "hours"))
}

func countryMismatchMessage(in Input, _ RuleConfig) string {
	ip := ""
	if in.IPCountry != nil {
		ip = *in.IPCountry
	}
	return fmt.Sprintf("IP country %s differs from card country %s", ip, in.CardCountry)
}

// BlocklistMatch is the fired entry of a purchase blocked by the blocklist. Its
// message names the type of the matched entry, never its value, which can be a
// card token or an IP address.
func BlocklistMatch(entryType string) FiredRule {
	label := entryType
	if entryType == "ip" {
		label = "IP address"
	}
	return FiredRule{Name: RuleBlocklist, Message: fmt.Sprintf("Blocked: the %s is on the blocklist", label)}
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
