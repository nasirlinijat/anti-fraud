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
    purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, model_score
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
);

-- name: ListReviewQueue :many
SELECT d.purchase_id, p.transaction_id, p.event_time, p.amount_minor, p.player_id,
       d.rule_score, d.model_score, d.fired_rules, d.decided_at
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.outcome = 'review'
  AND (
      sqlc.narg(after_decided_at)::timestamptz IS NULL
      OR (d.decided_at, d.purchase_id) > (
          sqlc.narg(after_decided_at)::timestamptz, sqlc.narg(after_purchase_id)::bigint
      )
  )
ORDER BY d.decided_at, d.purchase_id
LIMIT sqlc.arg(page_limit);

-- name: CountCardPurchasesLastHour :one
-- Counts purchases with the card in the hour before the given time. Purchases
-- at exactly that time are already saved, so they come earlier and are counted.
SELECT count(*)
FROM purchases
WHERE card_token = sqlc.arg(card_token)
  AND event_time > sqlc.arg(event_time)::timestamptz - interval '1 hour'
  AND event_time <= sqlc.arg(event_time)::timestamptz;
