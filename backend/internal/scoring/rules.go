package scoring

// RuleName identifies a fraud rule.
type RuleName string

const (
	RuleHighAmount      RuleName = "high_amount"
	RuleCardVelocity    RuleName = "card_velocity"
	RuleNewAccount      RuleName = "new_account"
	RuleCountryMismatch RuleName = "country_mismatch"
)
