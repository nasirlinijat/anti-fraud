package rulesets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// ErrNotFound means no ruleset has the requested version.
var ErrNotFound = errors.New("ruleset version not found")

// VersionSummary is an entry in the ruleset version history.
type VersionSummary struct {
	Version   int       `json:"version"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

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
	return s.loadRuleset(ctx, q, row.Version, row.ReviewThreshold, row.BlockThreshold, row.Note, row.CreatedAt)
}

// Version returns the ruleset with the given version, or ErrNotFound.
func (s *Store) Version(ctx context.Context, version int32) (scoring.Ruleset, error) {
	q := db.New(s.conn)
	row, err := q.GetRuleset(ctx, version)
	if errors.Is(err, pgx.ErrNoRows) {
		return scoring.Ruleset{}, ErrNotFound
	}
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("get ruleset %d: %w", version, err)
	}
	return s.loadRuleset(ctx, q, row.Version, row.ReviewThreshold, row.BlockThreshold, row.Note, row.CreatedAt)
}

// List returns all ruleset versions, newest first.
func (s *Store) List(ctx context.Context) ([]VersionSummary, error) {
	rows, err := db.New(s.conn).ListRulesets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list rulesets: %w", err)
	}
	items := make([]VersionSummary, 0, len(rows))
	for _, r := range rows {
		items = append(items, VersionSummary{
			Version:   int(r.Version),
			Note:      r.Note,
			CreatedAt: r.CreatedAt.Time.UTC(),
		})
	}
	return items, nil
}

func (s *Store) loadRuleset(
	ctx context.Context, q *db.Queries,
	version, reviewThreshold, blockThreshold int32, note string, createdAt pgtype.Timestamptz,
) (scoring.Ruleset, error) {
	ruleRows, err := q.ListRulesetRules(ctx, version)
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("list rules of ruleset %d: %w", version, err)
	}

	rules := make(map[scoring.RuleName]scoring.RuleConfig, len(ruleRows))
	for _, r := range ruleRows {
		name := scoring.RuleName(r.RuleName)
		if !name.Known() {
			return scoring.Ruleset{}, fmt.Errorf("ruleset %d has unknown rule %q", version, r.RuleName)
		}
		rules[name] = scoring.RuleConfig{
			Enabled:          r.Enabled,
			Weight:           int(r.Weight),
			Limit:            r.RuleLimit,
			WindowDays:       r.WindowDays.Int32,
			MinAmountMinor:   r.MinAmountMinor.Int64,
			AmountMultiplier: r.AmountMultiplier.Int32,
		}
	}

	return scoring.Ruleset{
		Version:         int(version),
		ReviewThreshold: int(reviewThreshold),
		BlockThreshold:  int(blockThreshold),
		Note:            note,
		CreatedAt:       createdAt.Time.UTC(),
		Rules:           rules,
	}, nil
}

// Publish saves a new version and activates it in the same transaction.
func (s *Store) Publish(ctx context.Context, rs scoring.Ruleset, operatorID string) (scoring.Ruleset, error) {
	err := pgx.BeginFunc(ctx, s.conn, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.LockRulesetsForPublish(ctx); err != nil {
			return fmt.Errorf("lock rulesets: %w", err)
		}
		if err := q.DeactivateRuleset(ctx); err != nil {
			return fmt.Errorf("deactivate ruleset: %w", err)
		}
		saved, err := q.InsertRuleset(ctx, db.InsertRulesetParams{
			ReviewThreshold: int32(rs.ReviewThreshold),
			BlockThreshold:  int32(rs.BlockThreshold),
			Note:            rs.Note,
		})
		if err != nil {
			return fmt.Errorf("insert ruleset: %w", err)
		}
		for name, rule := range rs.Rules {
			spike := name == scoring.RuleNewDeviceSpike
			if err := q.InsertRulesetRule(ctx, db.InsertRulesetRuleParams{
				RulesetVersion:   saved.Version,
				RuleName:         string(name),
				Enabled:          rule.Enabled,
				Weight:           int32(rule.Weight),
				RuleLimit:        rule.Limit,
				WindowDays:       pgtype.Int4{Int32: rule.WindowDays, Valid: spike},
				MinAmountMinor:   pgtype.Int8{Int64: rule.MinAmountMinor, Valid: spike},
				AmountMultiplier: pgtype.Int4{Int32: rule.AmountMultiplier, Valid: spike},
			}); err != nil {
				return fmt.Errorf("insert rule %q: %w", name, err)
			}
		}
		if err := q.AuditRulesetPublication(ctx, db.AuditRulesetPublicationParams{
			OperatorID: operatorID,
			Version:    int64(saved.Version),
		}); err != nil {
			return fmt.Errorf("record ruleset publication: %w", err)
		}
		rs.Version = int(saved.Version)
		rs.CreatedAt = saved.CreatedAt.Time.UTC()
		return nil
	})
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("publish ruleset: %w", err)
	}
	return rs, nil
}
