package scoring

import (
	"fmt"
	"time"
)

// AccountHistory is the player's account activity before a purchase. It is
// read only from account events with an event time before the purchase.
type AccountHistory struct {
	// LastPasswordReset is the latest password reset, or nil without one.
	LastPasswordReset *time.Time
	// NewDeviceLogins and NewCountryLogins list the first logins from a device
	// or a country the player had not used before, newest first. A device or
	// country is known from earlier logins and earlier trusted purchases.
	// Nothing is new for a login without the value, or for a player whose
	// earlier activity never had it, because there is nothing to compare with.
	NewDeviceLogins  []time.Time
	NewCountryLogins []time.Time
}

// UsesAccountEvents reports whether the rule reads the player's account
// events. The limit of each such rule is a window in hours.
func (n RuleName) UsesAccountEvents() bool {
	return n == RuleRecentPasswordReset || n == RuleNewDeviceLogin || n == RuleNewCountryLogin
}

// AccountEventLookback returns how far before a purchase the enabled rules
// read account events, or zero when no enabled rule reads them.
func AccountEventLookback(rs Ruleset) time.Duration {
	var hours int64
	for name, cfg := range rs.Rules {
		if cfg.Enabled && name.UsesAccountEvents() {
			hours = max(hours, cfg.Limit)
		}
	}
	return time.Duration(hours) * time.Hour
}

// recentPasswordReset takes limit in hours.
func recentPasswordReset(in Input, cfg RuleConfig) bool {
	if in.AccountHistory == nil || in.AccountHistory.LastPasswordReset == nil {
		return false
	}
	return withinHoursBefore(*in.AccountHistory.LastPasswordReset, in.EventTime, cfg.Limit)
}

// newDeviceLogin takes limit in hours.
func newDeviceLogin(in Input, cfg RuleConfig) bool {
	_, found := latestNewLogin(in, cfg.Limit, func(h *AccountHistory) []time.Time { return h.NewDeviceLogins })
	return found
}

// newCountryLogin takes limit in hours.
func newCountryLogin(in Input, cfg RuleConfig) bool {
	_, found := latestNewLogin(in, cfg.Limit, func(h *AccountHistory) []time.Time { return h.NewCountryLogins })
	return found
}

// latestNewLogin returns the latest of the logins that list picks from the
// history within the window.
func latestNewLogin(in Input, hours int64, list func(*AccountHistory) []time.Time) (time.Time, bool) {
	if in.AccountHistory == nil {
		return time.Time{}, false
	}
	var latest time.Time
	found := false
	for _, t := range list(in.AccountHistory) {
		if withinHoursBefore(t, in.EventTime, hours) && (!found || t.After(latest)) {
			latest, found = t, true
		}
	}
	return latest, found
}

// withinHoursBefore reports whether t is before eventTime by less than the
// given hours. An event at the same time as the purchase is not before it,
// and one exactly the window earlier is outside it.
func withinHoursBefore(t, eventTime time.Time, hours int64) bool {
	age := eventTime.Sub(t)
	return age > 0 && age < time.Duration(hours)*time.Hour
}

func recentPasswordResetMessage(in Input, cfg RuleConfig) string {
	return fmt.Sprintf("Password reset %s before the purchase (limit %d %s)",
		ageText(*in.AccountHistory.LastPasswordReset, in.EventTime), cfg.Limit, plural(cfg.Limit, "hour", "hours"))
}

func newDeviceLoginMessage(in Input, cfg RuleConfig) string {
	login, _ := latestNewLogin(in, cfg.Limit, func(h *AccountHistory) []time.Time { return h.NewDeviceLogins })
	return fmt.Sprintf("Login from a device the player had not used before, %s before the purchase (limit %d %s)",
		ageText(login, in.EventTime), cfg.Limit, plural(cfg.Limit, "hour", "hours"))
}

func newCountryLoginMessage(in Input, cfg RuleConfig) string {
	login, _ := latestNewLogin(in, cfg.Limit, func(h *AccountHistory) []time.Time { return h.NewCountryLogins })
	return fmt.Sprintf("Login from a country the player had not used before, %s before the purchase (limit %d %s)",
		ageText(login, in.EventTime), cfg.Limit, plural(cfg.Limit, "hour", "hours"))
}

// ageText states how long before eventTime t was, in whole minutes under an
// hour and in whole hours otherwise.
func ageText(t, eventTime time.Time) string {
	age := eventTime.Sub(t)
	if age < time.Hour {
		minutes := int64(age / time.Minute)
		return fmt.Sprintf("%d %s", minutes, plural(minutes, "minute", "minutes"))
	}
	hours := int64(age / time.Hour)
	return fmt.Sprintf("%d %s", hours, plural(hours, "hour", "hours"))
}
