-- name: PlayerHistory :one
-- Summarizes the player's purchases that come before the given one in
-- (event_time, transaction_id) order.
SELECT
    count(*) AS purchases,
    coalesce(sum(amount_minor), 0)::bigint AS amount_total_minor,
    coalesce(max(amount_minor), 0)::bigint AS amount_max_minor,
    min(event_time)::timestamptz AS first_time,
    max(event_time)::timestamptz AS last_time,
    count(*) FILTER (WHERE event_time > sqlc.arg(event_time)::timestamptz - interval '3600 seconds') AS uses_1h,
    count(*) FILTER (WHERE event_time > sqlc.arg(event_time)::timestamptz - interval '86400 seconds') AS uses_24h,
    count(*) FILTER (WHERE amount_minor = sqlc.arg(amount_minor)::bigint) AS same_amount,
    count(DISTINCT card_token) AS distinct_cards,
    count(DISTINCT coalesce(email_domain, 'none')) AS distinct_emails,
    count(*) FILTER (WHERE coalesce(email_domain, 'none') = sqlc.arg(email_domain)::text) AS email_uses,
    count(DISTINCT device_name) AS distinct_devices,
    count(*) FILTER (WHERE device_name = sqlc.narg(device_name)::text) AS device_uses,
    count(*) FILTER (WHERE client_os = sqlc.narg(client_os)::text) AS os_uses,
    count(*) FILTER (WHERE client_browser = sqlc.narg(client_browser)::text) AS browser_uses,
    count(*) FILTER (WHERE screen_resolution = sqlc.narg(screen_resolution)::text) AS screen_uses,
    count(DISTINCT client_browser) AS distinct_browsers
FROM purchases
WHERE player_id = sqlc.arg(player_id)
  AND (event_time, transaction_id) < (sqlc.arg(event_time)::timestamptz, sqlc.arg(transaction_id)::text);

-- name: CardHistory :one
-- Summarizes the card's purchases that come before the given one.
SELECT
    count(*) FILTER (WHERE event_time > sqlc.arg(event_time)::timestamptz - interval '3600 seconds') AS uses_1h,
    count(*) FILTER (WHERE event_time > sqlc.arg(event_time)::timestamptz - interval '86400 seconds') AS uses_24h,
    count(DISTINCT player_id) AS players
FROM purchases
WHERE card_token = sqlc.arg(card_token)
  AND (event_time, transaction_id) < (sqlc.arg(event_time)::timestamptz, sqlc.arg(transaction_id)::text);

-- name: IssuerHistory :one
-- Summarizes the card issuer's purchases that come before the given one. A
-- missing issuer is grouped as the empty string.
WITH issuer_purchases AS (
    SELECT player_id, billing_region, email_domain
    FROM purchases
    WHERE coalesce(card_issuer, '') = sqlc.arg(card_issuer)::text
      AND (event_time, transaction_id) < (sqlc.arg(event_time)::timestamptz, sqlc.arg(transaction_id)::text)
      AND issuer_history_fits_index(card_issuer, transaction_id, player_id, billing_region, email_domain)
    UNION ALL
    SELECT player_id, billing_region, email_domain
    FROM purchases
    WHERE coalesce(card_issuer, '') = sqlc.arg(card_issuer)::text
      AND (event_time, transaction_id) < (sqlc.arg(event_time)::timestamptz, sqlc.arg(transaction_id)::text)
      AND NOT issuer_history_fits_index(card_issuer, transaction_id, player_id, billing_region, email_domain)
)
SELECT
    count(DISTINCT player_id) AS players,
    count(DISTINCT coalesce(billing_region, 'none')) AS billing_regions,
    count(DISTINCT coalesce(email_domain, 'none')) AS email_domains
FROM issuer_purchases;

-- name: FeatureCounts :many
-- Reads the running totals for all purchases, the email domain, the device name
-- and the client OS, browser and screen.
SELECT kind, key, purchases, players
FROM feature_counts
WHERE (kind = 'all' AND key = '')
   OR (kind = 'email' AND key = sqlc.arg(email_domain)::text)
   OR (kind = 'device' AND key = sqlc.narg(device_name)::text)
   OR (kind = 'os' AND key = sqlc.narg(client_os)::text)
   OR (kind = 'browser' AND key = sqlc.narg(client_browser)::text)
   OR (kind = 'screen' AND key = sqlc.narg(screen_resolution)::text);

-- name: AddFeatureCount :exec
-- Adds one purchase to a running total. new_player is 1 when the player had no
-- earlier purchase counted in this total.
INSERT INTO feature_counts (kind, key, purchases, players)
VALUES (sqlc.arg(kind), sqlc.arg(key), 1, sqlc.arg(new_player)::bigint)
ON CONFLICT (kind, key) DO UPDATE
SET purchases = feature_counts.purchases + 1,
    players = feature_counts.players + excluded.players;
