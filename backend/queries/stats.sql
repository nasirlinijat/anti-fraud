-- name: CountOutcomes :many
-- Counts the decisions made since the given time by outcome.
SELECT outcome, count(*) AS decisions
FROM decisions
WHERE decided_at >= sqlc.arg(since)::timestamptz
GROUP BY outcome;

-- name: TopFiredRules :many
-- Lists the rules that fired most often in decisions made since the given time.
SELECT
    rule.name::text AS name,
    count(*) AS fired,
    sum(rule.points)::bigint AS points
FROM decisions d
CROSS JOIN LATERAL jsonb_to_recordset(d.fired_rules) AS rule(name text, points integer)
WHERE d.decided_at >= sqlc.arg(since)::timestamptz
GROUP BY rule.name
ORDER BY fired DESC, name
LIMIT sqlc.arg(max_rules);

-- name: CountReviewQueue :one
-- Counts the purchases waiting in the review queue.
SELECT count(*)
FROM decisions d
WHERE d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id);

-- name: CountModelEffects :one
-- Counts, since the given time, the decisions scored by the model, the allowed
-- purchases the model sent to review, and the reviews it cleared. The rule-only
-- outcome comes from the rule score and the ruleset thresholds.
SELECT
    count(d.model_score) AS scored,
    count(*) FILTER (WHERE d.outcome = 'review' AND d.rule_score < r.review_threshold) AS flagged,
    count(*) FILTER (WHERE d.outcome = 'allow' AND d.rule_score >= r.review_threshold) AS cleared
FROM decisions d
JOIN rulesets r ON r.version = d.ruleset_version
WHERE d.decided_at >= sqlc.arg(since)::timestamptz;
