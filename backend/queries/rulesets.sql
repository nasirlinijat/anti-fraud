-- name: GetActiveRuleset :one
SELECT version, review_threshold, block_threshold
FROM rulesets
WHERE is_active;

-- name: ListRulesetRules :many
SELECT rule_name, enabled, weight, rule_limit
FROM ruleset_rules
WHERE ruleset_version = $1;
