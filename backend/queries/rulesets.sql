-- name: GetActiveRuleset :one
SELECT version, review_threshold, block_threshold
FROM rulesets
WHERE is_active;

-- name: ListRulesetRules :many
SELECT rule_name, enabled, weight, rule_limit, window_days, min_amount_minor, amount_multiplier
FROM ruleset_rules
WHERE ruleset_version = $1;

-- name: LockRulesetsForPublish :exec
-- Serialize publishers without blocking readers of the current ruleset.
LOCK TABLE rulesets IN SHARE ROW EXCLUSIVE MODE;

-- name: DeactivateRuleset :exec
UPDATE rulesets SET is_active = false WHERE is_active;

-- name: InsertRuleset :one
INSERT INTO rulesets (version, review_threshold, block_threshold, is_active)
SELECT COALESCE(MAX(version), 0) + 1, $1, $2, true
FROM rulesets
RETURNING version;

-- name: InsertRulesetRule :exec
INSERT INTO ruleset_rules (
    ruleset_version, rule_name, enabled, weight, rule_limit, window_days, min_amount_minor, amount_multiplier
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);
