-- name: InsertPurchase :one
INSERT INTO purchases (
    transaction_id, event_time, amount_minor, player_id, card_token,
    card_issuer, card_network, card_type, card_country, card_category,
    product_code, billing_region, billing_country, billing_distance,
    email_domain, recipient_email_domain, device_type, device_name, device_id,
    ip_address, ip_country, account_created_at,
    client_os, client_browser, screen_resolution
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17, $18, $19,
    $20, $21, $22,
    $23, $24, $25
)
RETURNING id;

-- name: InsertDecision :exec
INSERT INTO decisions (
    purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, model_score,
    model_score_unblended, scoring_latency_us, model_version
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10
);

-- name: ListReviewQueue :many
SELECT d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id,
       d.rule_score, d.model_score, d.model_version, d.fired_rules, d.decided_at
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)
  AND (
      sqlc.narg(after_decided_at)::timestamptz IS NULL
      OR (d.decided_at, d.purchase_id) > (
          sqlc.narg(after_decided_at)::timestamptz, sqlc.narg(after_purchase_id)::bigint
      )
  )
ORDER BY d.decided_at, d.purchase_id
LIMIT sqlc.arg(page_limit);

-- name: GetReviewCase :one
SELECT d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id,
       d.ruleset_version, d.rule_score, d.model_score, d.model_version, d.fired_rules, d.decided_at
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.purchase_id = sqlc.arg(purchase_id)
  AND d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id);

-- name: CountCardPurchasesLastHour :one
-- Counts purchases with the card in the hour before the given time. Purchases
-- at exactly that time are already saved, so they come earlier and are counted.
SELECT count(*)
FROM purchases
WHERE card_token = sqlc.arg(card_token)
  AND event_time > sqlc.arg(event_time)::timestamptz - interval '1 hour'
  AND event_time <= sqlc.arg(event_time)::timestamptz;

-- name: PlayerEarlierModelScores :one
-- Counts and sums the unblended model scores of the player's purchases that
-- come before the given one in (event_time, transaction_id) order.
SELECT
    count(d.model_score_unblended) AS scored,
    coalesce(sum(d.model_score_unblended ORDER BY p.event_time, p.transaction_id), 0)::float8 AS score_sum
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
WHERE p.player_id = sqlc.arg(player_id)
  AND (p.event_time, p.transaction_id) < (sqlc.arg(event_time)::timestamptz, sqlc.arg(transaction_id)::text);
