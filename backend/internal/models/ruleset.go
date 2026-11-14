package models

import (
	"errors"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// ErrRulesetNotFound means no ruleset has the requested version.
var ErrRulesetNotFound = errors.New("ruleset version not found")

// RulesetSummary is an entry in the ruleset version history.
type RulesetSummary struct {
	Version   int       `json:"version"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

type ReplayDecision struct {
	PurchaseID     int64
	TransactionID  string
	DecidedAt      time.Time
	Outcome        string
	RuleScore      int
	FiredRules     []scoring.FiredRule
	ModelScore     *float64
	RulesetVersion int
}

type PreviewCounts struct {
	Allow  int `json:"allow"`
	Review int `json:"review"`
	Block  int `json:"block"`
}

func (c *PreviewCounts) Add(outcome string) {
	switch outcome {
	case "allow":
		c.Allow++
	case "review":
		c.Review++
	case "block":
		c.Block++
	}
}

type PreviewChange struct {
	PurchaseID      int64
	TransactionID   string
	DecidedAt       time.Time
	Before, After   string
	RuleScoreBefore int
	RuleScoreAfter  int
	Uncertain       bool
}

type RulesetPreview struct {
	Decisions          int
	Before, After      PreviewCounts
	Changed            int
	Uncertain          int
	UncertainRules     []string
	MissingModelScores int
	Changes            []PreviewChange
}
