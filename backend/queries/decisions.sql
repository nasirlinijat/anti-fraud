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

-- name: GetSavedScore :one
SELECT
    ROW(
        p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.card_token,
        p.card_issuer, p.card_network, p.card_type, p.card_country, p.card_category,
        p.product_code, p.billing_region, p.billing_country, p.billing_distance,
        p.email_domain, p.recipient_email_domain, p.device_type, p.device_name, p.device_id,
        p.ip_address, p.ip_country, p.account_created_at,
        p.client_os, p.client_browser, p.screen_resolution
    ) IS NOT DISTINCT FROM ROW(
        sqlc.arg(transaction_id)::text, sqlc.arg(event_time)::timestamptz, sqlc.arg(amount_minor)::bigint,
        sqlc.arg(player_id)::text, sqlc.arg(card_token)::text,
        sqlc.narg(card_issuer)::text, sqlc.narg(card_network)::text, sqlc.narg(card_type)::text,
        sqlc.narg(card_country)::text, sqlc.narg(card_category)::text, sqlc.narg(product_code)::text,
        sqlc.narg(billing_region)::text, sqlc.narg(billing_country)::text, sqlc.narg(billing_distance)::float8,
        sqlc.narg(email_domain)::text, sqlc.narg(recipient_email_domain)::text,
        sqlc.narg(device_type)::text, sqlc.narg(device_name)::text, sqlc.narg(device_id)::text,
        sqlc.narg(ip_address)::inet, sqlc.narg(ip_country)::text, sqlc.narg(account_created_at)::timestamptz,
        sqlc.narg(client_os)::text, sqlc.narg(client_browser)::text, sqlc.narg(screen_resolution)::text
    ) AS request_matches,
    d.outcome, d.rule_score, d.fired_rules, d.ruleset_version, d.model_score, d.model_version
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
WHERE p.transaction_id = sqlc.arg(transaction_id);

-- name: ListReviewQueue :many
SELECT d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.device_id,
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
SELECT d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.device_id,
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

-- name: CountDeviceDistinctCardsLastHour :one
-- Include the current card exactly once, even if it already appears in history.
SELECT (count(DISTINCT card_token) + 1)::bigint AS card_count
FROM purchases
WHERE device_id IS NOT NULL
  AND device_id = sqlc.arg(device_id)::text
  AND event_time > sqlc.arg(event_time)::timestamptz - interval '1 hour'
  AND event_time <= sqlc.arg(event_time)::timestamptz
  AND card_token <> sqlc.arg(card_token)::text;

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
