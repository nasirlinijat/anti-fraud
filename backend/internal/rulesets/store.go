package rulesets

import (
	"context"
	"fmt"

	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// Store reads rulesets from the database.
type Store struct {
	q *db.Queries
}

func NewStore(conn db.DBTX) *Store {
	return &Store{q: db.New(conn)}
}

// Active returns the active ruleset with its rules.
func (s *Store) Active(ctx context.Context) (scoring.Ruleset, error) {
	row, err := s.q.GetActiveRuleset(ctx)
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("get active ruleset: %w", err)
	}

	ruleRows, err := s.q.ListRulesetRules(ctx, row.Version)
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("list rules of ruleset %d: %w", row.Version, err)
	}

	rules := make(map[scoring.RuleName]scoring.RuleConfig, len(ruleRows))
	for _, r := range ruleRows {
		name := scoring.RuleName(r.RuleName)
		if !name.Known() {
			return scoring.Ruleset{}, fmt.Errorf("ruleset %d has unknown rule %q", row.Version, r.RuleName)
		}
		rules[name] = scoring.RuleConfig{
			Enabled: r.Enabled,
			Weight:  int(r.Weight),
			Limit:   r.RuleLimit,
		}
	}

	return scoring.Ruleset{
		Version:         int(row.Version),
		ReviewThreshold: int(row.ReviewThreshold),
		BlockThreshold:  int(row.BlockThreshold),
		Rules:           rules,
	}, nil
}
