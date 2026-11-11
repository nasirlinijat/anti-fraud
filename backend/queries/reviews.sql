-- name: ListReviewQueue :many
SELECT d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.device_id,
       d.rule_score, d.model_score, d.model_version, d.fired_rules, d.decided_at
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)
  AND (
      sqlc.narg(after_score)::integer IS NULL
      OR (-d.rule_score, d.decided_at, d.purchase_id) > (
          sqlc.narg(after_score)::integer, sqlc.narg(after_decided_at)::timestamptz, sqlc.narg(after_purchase_id)::bigint
      )
  )
ORDER BY -d.rule_score, d.decided_at, d.purchase_id
LIMIT sqlc.arg(page_limit);

-- name: ListReviewQueueByModel :many
SELECT d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.device_id,
       d.rule_score, d.model_score, d.model_version, d.fired_rules, d.decided_at
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)
  AND (
      sqlc.narg(after_score)::double precision IS NULL
      OR (COALESCE(-d.model_score, 1::double precision), d.decided_at, d.purchase_id) > (
          sqlc.narg(after_score)::double precision, sqlc.narg(after_decided_at)::timestamptz, sqlc.narg(after_purchase_id)::bigint
      )
  )
ORDER BY COALESCE(-d.model_score, 1::double precision), d.decided_at, d.purchase_id
LIMIT sqlc.arg(page_limit);

-- name: ResolveReview :one
WITH candidate AS (
    SELECT d.purchase_id, d.outcome
    FROM decisions d
    WHERE d.purchase_id = sqlc.arg(target_purchase_id)
), saved AS (
    INSERT INTO review_resolutions (purchase_id, decision, analyst_id)
    SELECT candidate.purchase_id, sqlc.arg(decision)::text, sqlc.arg(analyst_id)::text
    FROM candidate
    WHERE candidate.outcome = 'review'
    ON CONFLICT (purchase_id) DO NOTHING
    RETURNING resolved_at
)
SELECT CASE
    WHEN EXISTS (SELECT 1 FROM saved) THEN 'saved'
    WHEN NOT EXISTS (SELECT 1 FROM candidate) THEN 'not_found'
    WHEN EXISTS (SELECT 1 FROM candidate WHERE outcome = 'review') THEN 'already_resolved'
    ELSE 'not_review'
END::text AS status,
(SELECT resolved_at FROM saved)::timestamptz AS resolved_at;

-- name: GetPurchaseDeviceID :one
-- Returns the device ID of a purchase, or NULL when it carried none.
SELECT device_id FROM purchases WHERE id = sqlc.arg(purchase_id);

-- name: ListCasePlayerPurchases :many
-- Lists the player's recent purchases before the given one, newest first.
SELECT p.transaction_id, p.event_time, p.amount_minor, d.outcome
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
WHERE p.player_id = sqlc.arg(player_id)
  AND (p.event_time, p.transaction_id) < (sqlc.arg(event_time)::timestamptz, sqlc.arg(transaction_id)::text)
ORDER BY p.event_time DESC, p.transaction_id DESC
LIMIT sqlc.arg(page_limit);

-- name: GetCaseDeviceActivity :one
-- Summarizes activity on the same device up to the given purchase.
SELECT
    count(*) AS purchases,
    count(DISTINCT card_token) AS distinct_cards,
    max(event_time)::timestamptz AS last_seen_at
FROM purchases
WHERE device_id IS NOT NULL
  AND device_id = sqlc.arg(device_id)::text
  AND (event_time, transaction_id) <= (sqlc.arg(event_time)::timestamptz, sqlc.arg(transaction_id)::text);
