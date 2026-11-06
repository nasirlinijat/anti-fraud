package scoring

import "time"

// RuleName identifies a fraud rule.
type RuleName string

const (
	RuleHighAmount      RuleName = "high_amount"
	RuleCardVelocity    RuleName = "card_velocity"
	RuleNewAccount      RuleName = "new_account"
	RuleCountryMismatch RuleName = "country_mismatch"
)

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
	if in.IPCountry == nil {
		return false
	}
	return *in.IPCountry != in.CardCountry
}
