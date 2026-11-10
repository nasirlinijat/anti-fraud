package scoring

import (
	"fmt"
	"time"
)

// RuleName identifies a fraud rule.
type RuleName string

const (
	RuleHighAmount      RuleName = "high_amount"
	RuleCardVelocity    RuleName = "card_velocity"
	RuleNewAccount      RuleName = "new_account"
	RuleCountryMismatch RuleName = "country_mismatch"

	// RuleBlocklist names the entry of a purchase that matched the blocklist.
	// It is not a configurable rule, so Known does not report it.
	RuleBlocklist RuleName = "blocklist"
)

// rules lists the configurable rules in evaluation order. Each has a check and
// a message that states the value that made it fire and its limit.
var rules = []struct {
	name    RuleName
	check   func(Input, int64) bool
	message func(Input, int64) string
}{
	{RuleHighAmount, highAmount, highAmountMessage},
	{RuleCardVelocity, cardVelocity, cardVelocityMessage},
	{RuleNewAccount, newAccount, newAccountMessage},
	{RuleCountryMismatch, countryMismatch, countryMismatchMessage},
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

func highAmount(in Input, limit int64) bool {
	return in.AmountMinor >= limit
}

func cardVelocity(in Input, limit int64) bool {
	return int64(in.CardPurchasesLastHour) >= limit
}

// newAccount takes limit in hours.
func newAccount(in Input, limit int64) bool {
	if in.AccountCreatedAt == nil {
		return false
	}
	return in.EventTime.Sub(*in.AccountCreatedAt) < time.Duration(limit)*time.Hour
}

func countryMismatch(in Input, _ int64) bool {
	if in.IPCountry == nil || in.CardCountry == "" {
		return false
	}
	return *in.IPCountry != in.CardCountry
}

func highAmountMessage(in Input, limit int64) string {
	return fmt.Sprintf("Amount %d minor units is at or above the limit of %d", in.AmountMinor, limit)
}

func cardVelocityMessage(in Input, limit int64) string {
	return fmt.Sprintf("Card used %d %s in 60 minutes (limit %d)", in.CardPurchasesLastHour, plural(int64(in.CardPurchasesLastHour), "time", "times"), limit)
}

func newAccountMessage(in Input, limit int64) string {
	age := time.Duration(0)
	if in.AccountCreatedAt != nil {
		age = max(in.EventTime.Sub(*in.AccountCreatedAt), 0)
	}
	hours := int64(age / time.Hour)
	return fmt.Sprintf("Account is %d %s old, under the limit of %d %s", hours, plural(hours, "hour", "hours"), limit, plural(limit, "hour", "hours"))
}

func countryMismatchMessage(in Input, _ int64) string {
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
