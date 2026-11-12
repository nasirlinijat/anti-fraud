package models

import (
	"errors"
	"time"
)

// ErrRulesetNotFound means no ruleset has the requested version.
var ErrRulesetNotFound = errors.New("ruleset version not found")

// RulesetSummary is an entry in the ruleset version history.
type RulesetSummary struct {
	Version   int       `json:"version"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}
