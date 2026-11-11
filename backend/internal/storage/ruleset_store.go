package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// RulesetStore reads and publishes rulesets.
type RulesetStore struct {
	conn Conn
}

func NewRulesetStore(conn Conn) *RulesetStore {
	return &RulesetStore{conn: conn}
}

// rulesetColumns are the columns scanRuleset reads, in order.
const rulesetColumns = `version, review_threshold, block_threshold, note,
       model_mode, model_clear_below, model_flag_at, created_at`

const getActiveRuleset = `SELECT ` + rulesetColumns + `
FROM rulesets
WHERE is_active`

const getRuleset = `SELECT ` + rulesetColumns + `
FROM rulesets
WHERE version = $1`

// scanRuleset reads a rulesets row without its rules.
func scanRuleset(row pgx.Row) (scoring.Ruleset, error) {
	var rs scoring.Ruleset
	err := row.Scan(
		&rs.Version, &rs.ReviewThreshold, &rs.BlockThreshold, &rs.Note,
		&rs.Model.Mode, &rs.Model.ClearBelow, &rs.Model.FlagAt, &rs.CreatedAt,
	)
	if rs.Model.ClearBelow == nil || rs.Model.FlagAt == nil {
		rs.Model.ClearBelow, rs.Model.FlagAt = nil, nil
	}
	rs.CreatedAt = rs.CreatedAt.UTC()
	return rs, err
}

// Active returns the active ruleset with its rules.
func (s *RulesetStore) Active(ctx context.Context) (scoring.Ruleset, error) {
	rs, err := scanRuleset(s.conn.QueryRow(ctx, getActiveRuleset))
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("get active ruleset: %w", err)
	}
	return loadRules(ctx, s.conn, rs)
}

// Version returns the ruleset with the given version, or models.ErrRulesetNotFound.
func (s *RulesetStore) Version(ctx context.Context, version int32) (scoring.Ruleset, error) {
	rs, err := scanRuleset(s.conn.QueryRow(ctx, getRuleset, version))
	if errors.Is(err, pgx.ErrNoRows) {
		return scoring.Ruleset{}, models.ErrRulesetNotFound
	}
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("get ruleset %d: %w", version, err)
	}
	return loadRules(ctx, s.conn, rs)
}

const listRulesets = `
SELECT version, note, created_at
FROM rulesets
ORDER BY version DESC`

// List returns all ruleset versions, newest first.
func (s *RulesetStore) List(ctx context.Context) ([]models.RulesetSummary, error) {
	rows, err := s.conn.Query(ctx, listRulesets)
	if err != nil {
		return nil, fmt.Errorf("list rulesets: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.RulesetSummary, error) {
		var v models.RulesetSummary
		err := row.Scan(&v.Version, &v.Note, &v.CreatedAt)
		v.CreatedAt = v.CreatedAt.UTC()
		return v, err
	})
	if err != nil {
		return nil, fmt.Errorf("list rulesets: %w", err)
	}
	return items, nil
}

const listRulesetRules = `
SELECT rule_name, enabled, weight, rule_limit, window_days, min_amount_minor, amount_multiplier
FROM ruleset_rules
WHERE ruleset_version = $1`

// loadRules adds the rules to a ruleset read from the rulesets table.
func loadRules(ctx context.Context, conn platform.DBTX, rs scoring.Ruleset) (scoring.Ruleset, error) {
	rows, err := conn.Query(ctx, listRulesetRules, rs.Version)
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("list rules of ruleset %d: %w", rs.Version, err)
	}
	defer rows.Close()

	rules := make(map[scoring.RuleName]scoring.RuleConfig)
	for rows.Next() {
		var (
			name             scoring.RuleName
			cfg              scoring.RuleConfig
			windowDays       *int32
			minAmountMinor   *int64
			amountMultiplier *int32
		)
		if err := rows.Scan(&name, &cfg.Enabled, &cfg.Weight, &cfg.Limit, &windowDays, &minAmountMinor, &amountMultiplier); err != nil {
			return scoring.Ruleset{}, fmt.Errorf("read rule of ruleset %d: %w", rs.Version, err)
		}
		if !name.Known() {
			return scoring.Ruleset{}, fmt.Errorf("ruleset %d has unknown rule %q", rs.Version, name)
		}
		if windowDays != nil {
			cfg.WindowDays = *windowDays
		}
		if minAmountMinor != nil {
			cfg.MinAmountMinor = *minAmountMinor
		}
		if amountMultiplier != nil {
			cfg.AmountMultiplier = *amountMultiplier
		}
		rules[name] = cfg
	}
	if err := rows.Err(); err != nil {
		return scoring.Ruleset{}, fmt.Errorf("list rules of ruleset %d: %w", rs.Version, err)
	}
	rs.Rules = rules
	return rs, nil
}

const (
	// lockRulesets serializes publishers without blocking readers of the
	// current ruleset.
	lockRulesets      = `SELECT pg_advisory_xact_lock(27149, 1)`
	deactivateRuleset = `UPDATE rulesets SET is_active = false WHERE is_active`
	insertRuleset     = `
INSERT INTO rulesets (
    version, review_threshold, block_threshold, note, model_mode, model_clear_below, model_flag_at, is_active
)
SELECT COALESCE(MAX(version), 0) + 1, $1, $2, $3, $4, $5, $6, true
FROM rulesets
RETURNING version, created_at`
	insertRulesetRule = `
INSERT INTO ruleset_rules (
    ruleset_version, rule_name, enabled, weight, rule_limit, window_days, min_amount_minor, amount_multiplier
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	auditPublication = `
INSERT INTO configuration_audit_events (actor_kind, actor_id, action, resource_id)
VALUES ('operator', $1, 'ruleset.publish', $2)`
)

// Publish saves a new version and activates it in the same transaction. A
// ruleset without a model mode keeps the model settings of the active version,
// read under the same lock so concurrent publishers cannot copy stale settings.
// Thresholds that are still missing are taken from reportThresholds, the
// loaded model's report, when it is set.
func (s *RulesetStore) Publish(ctx context.Context, rs scoring.Ruleset, operatorID string, reportThresholds *scoring.ModelSettings) (scoring.Ruleset, error) {
	err := pgx.BeginFunc(ctx, s.conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, lockRulesets); err != nil {
			return fmt.Errorf("lock rulesets: %w", err)
		}
		if rs.Model.Mode == "" {
			active, err := scanRuleset(tx.QueryRow(ctx, getActiveRuleset))
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				rs.Model = scoring.ModelSettings{Mode: scoring.ModelAssist}
			case err != nil:
				return fmt.Errorf("get active ruleset: %w", err)
			default:
				rs.Model = active.Model
			}
		}
		if (rs.Model.ClearBelow == nil || rs.Model.FlagAt == nil) && reportThresholds != nil {
			rs.Model.ClearBelow, rs.Model.FlagAt = reportThresholds.ClearBelow, reportThresholds.FlagAt
		}
		if _, err := tx.Exec(ctx, deactivateRuleset); err != nil {
			return fmt.Errorf("deactivate ruleset: %w", err)
		}
		var version int32
		err := tx.QueryRow(ctx, insertRuleset,
			int32(rs.ReviewThreshold), int32(rs.BlockThreshold), rs.Note,
			string(rs.Model.Mode), rs.Model.ClearBelow, rs.Model.FlagAt,
		).Scan(&version, &rs.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert ruleset: %w", err)
		}
		for name, rule := range rs.Rules {
			// Only the new-device spike rule has these settings; other rules store NULL.
			var windowDays, amountMultiplier *int32
			var minAmountMinor *int64
			if name == scoring.RuleNewDeviceSpike {
				windowDays, minAmountMinor, amountMultiplier = &rule.WindowDays, &rule.MinAmountMinor, &rule.AmountMultiplier
			}
			if _, err := tx.Exec(ctx, insertRulesetRule,
				version, string(name), rule.Enabled, int32(rule.Weight), rule.Limit,
				windowDays, minAmountMinor, amountMultiplier,
			); err != nil {
				return fmt.Errorf("insert rule %q: %w", name, err)
			}
		}
		if _, err := tx.Exec(ctx, auditPublication, operatorID, int64(version)); err != nil {
			return fmt.Errorf("record ruleset publication: %w", err)
		}
		rs.Version = int(version)
		rs.CreatedAt = rs.CreatedAt.UTC()
		return nil
	})
	if err != nil {
		return scoring.Ruleset{}, fmt.Errorf("publish ruleset: %w", err)
	}
	return rs, nil
}
