-- name: DecisionOverview :one
SELECT
    count(*) FILTER (WHERE outcome = 'allow') AS allowed,
    count(*) FILTER (WHERE outcome = 'review') AS reviewed,
    count(*) FILTER (WHERE outcome = 'block') AS blocked,
    count(*) AS total,
    count(scoring_latency_us) AS latency_samples,
    coalesce(percentile_disc(0.95) WITHIN GROUP (ORDER BY scoring_latency_us), 0)::bigint AS p95_latency_us,
    coalesce(min(decided_at), sqlc.arg(since)::timestamptz)::timestamptz AS first_decision_at
FROM decisions
WHERE decided_at >= sqlc.arg(since)::timestamptz
  AND decided_at < sqlc.arg(until)::timestamptz;

-- name: BlockedValue :one
SELECT coalesce(sum(p.amount_minor), 0)::text AS amount_minor
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.outcome = 'block'
  AND d.decided_at >= sqlc.arg(since)::timestamptz
  AND d.decided_at < sqlc.arg(until)::timestamptz;

-- name: CountDecisionBuckets :many
SELECT
    date_bin(sqlc.arg(bucket_width)::interval, decided_at, TIMESTAMPTZ '1970-01-01 00:00:00+00')::timestamptz AS bucket_start,
    count(*) FILTER (WHERE outcome = 'allow') AS allowed,
    count(*) FILTER (WHERE outcome = 'review') AS reviewed,
    count(*) FILTER (WHERE outcome = 'block') AS blocked
FROM decisions
WHERE decided_at >= sqlc.arg(since)::timestamptz
  AND decided_at < sqlc.arg(until)::timestamptz
GROUP BY bucket_start
ORDER BY bucket_start;

-- name: TopFiredRules :many
-- Lists the rules that fired most often in decisions made since the given time.
SELECT
    rule.name::text AS name,
    count(*) AS fired,
    sum(rule.points)::bigint AS points
FROM decisions d
CROSS JOIN LATERAL jsonb_to_recordset(d.fired_rules) AS rule(name text, points integer)
WHERE d.decided_at >= sqlc.arg(since)::timestamptz
  AND d.decided_at < sqlc.arg(until)::timestamptz
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
WHERE d.decided_at >= sqlc.arg(since)::timestamptz
  AND d.decided_at < sqlc.arg(until)::timestamptz;
