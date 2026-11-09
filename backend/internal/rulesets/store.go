package rulesets

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// Conn runs queries and starts transactions.
type Conn interface {
	db.DBTX
	Begin(context.Context) (pgx.Tx, error)
}

// Store reads and publishes rulesets.
type Store struct {
	conn Conn
}

func NewStore(conn Conn) *Store {
	return &Store{conn: conn}
}

// Active returns the active ruleset with its rules.
func (s *Store) Active(ctx context.Context) (scoring.Ruleset, error) {
	q := db.New(s.conn)
	row, err := q.GetActiveRuleset(ctx)
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("get active ruleset: %w", err)
	}

	ruleRows, err := q.ListRulesetRules(ctx, row.Version)
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

// Publish saves a new version and activates it in the same transaction.
func (s *Store) Publish(ctx context.Context, rs scoring.Ruleset) (scoring.Ruleset, error) {
	err := pgx.BeginFunc(ctx, s.conn, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.LockRulesetsForPublish(ctx); err != nil {
			return fmt.Errorf("lock rulesets: %w", err)
		}
		if err := q.DeactivateRuleset(ctx); err != nil {
			return fmt.Errorf("deactivate ruleset: %w", err)
		}
		version, err := q.InsertRuleset(ctx, db.InsertRulesetParams{
			ReviewThreshold: int32(rs.ReviewThreshold),
			BlockThreshold:  int32(rs.BlockThreshold),
		})
		if err != nil {
			return fmt.Errorf("insert ruleset: %w", err)
		}
		for name, rule := range rs.Rules {
			if err := q.InsertRulesetRule(ctx, db.InsertRulesetRuleParams{
				RulesetVersion: version,
				RuleName:       string(name),
				Enabled:        rule.Enabled,
				Weight:         int32(rule.Weight),
				RuleLimit:      rule.Limit,
			}); err != nil {
				return fmt.Errorf("insert rule %q: %w", name, err)
			}
		}
		rs.Version = int(version)
		return nil
	})
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("publish ruleset: %w", err)
	}
	return rs, nil
}
