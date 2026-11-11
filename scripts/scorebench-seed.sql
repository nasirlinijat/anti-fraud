BEGIN;

DO $$
BEGIN
    IF current_database() <> 'scorebench'
        OR EXISTS (SELECT 1 FROM purchases)
        OR EXISTS (SELECT 1 FROM decisions)
        OR EXISTS (SELECT 1 FROM feature_counts) THEN
        RAISE EXCEPTION 'scorebench requires a fresh benchmark database';
    END IF;
END
$$;

CREATE EXTENSION pg_stat_statements;

-- Each player uses two cards. The last 120 purchases put six distinct cards on
-- each of 20 shared devices within the last hour.
WITH slots AS (
    SELECT i, (i - 1) % 10000 AS player_no, ((i - 1) / 10000) % 2 AS card_no
    FROM generate_series(1, 500000) AS g(i)
)
INSERT INTO purchases (
    transaction_id, event_time, amount_minor, player_id, card_token, card_issuer,
    card_network, card_type, card_country, product_code, billing_region, billing_country,
    email_domain, device_type, device_name, device_id, ip_country, account_created_at,
    client_os, client_browser, screen_resolution
)
SELECT
    'bench-history-' || i,
    '2026-11-01T12:00:00Z'::timestamptz - (500001 - i) * interval '20.736 seconds',
    200 + (i * 97) % 2801,
    'bench-player-' || player_no,
    'bench-card-' || player_no || '-' || card_no,
    CASE WHEN player_no % 10 = 0 THEN NULL
         WHEN player_no % 10 = 1 THEN 'bench-common-issuer'
         ELSE 'bench-issuer-' || ((player_no * 7 + card_no) % 64)
    END,
    CASE WHEN card_no = 0 THEN 'visa' ELSE 'mastercard' END,
    'debit', 'US', 'W', (player_no % 121)::text, 'US',
    'example-' || (player_no % 21) || '.test',
    'desktop', 'bench-device-type-' || (player_no % 8),
    CASE WHEN i > 499880 THEN 'bench-shared-' || ((i - 499881) / 6)
         ELSE 'bench-device-' || player_no
    END,
    'US', '2025-11-01T12:00:00Z'::timestamptz,
    CASE WHEN player_no % 2 = 0 THEN 'Windows 10' ELSE 'Mac OS X 10_15_7' END,
    CASE WHEN player_no % 3 = 0 THEN 'safari 16.0' ELSE 'chrome 63.0' END,
    CASE WHEN player_no % 2 = 0 THEN '1920x1080' ELSE '1440x900' END
FROM slots
ORDER BY i;

-- Scores and outcomes are synthetic fixtures, with no training data involved.
INSERT INTO decisions (
    purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs,
    model_score, model_score_unblended, model_version, decided_at
)
SELECT id, 1,
       CASE (id + id / 10000) % 50
           WHEN 0 THEN 'block' WHEN 1 THEN 'review' WHEN 2 THEN 'review' ELSE 'allow'
       END,
       0, '[]', '{}', (id % 700)::float8 / 1000, (id % 700)::float8 / 1000,
       'scorebench-fixture', event_time
FROM purchases;

INSERT INTO review_resolutions (purchase_id, decision, analyst_id, resolved_at)
SELECT purchase_id, CASE (purchase_id / 10000) % 3 WHEN 0 THEN 'approve' ELSE 'reject' END,
       'scorebench-analyst', decided_at + interval '1 hour'
FROM decisions
WHERE outcome = 'review' AND (purchase_id / 10000) % 3 <> 2;

INSERT INTO feature_counts (kind, key, purchases, players)
SELECT dimensions.kind, dimensions.key, count(*), count(DISTINCT p.player_id)
FROM purchases p
CROSS JOIN LATERAL (VALUES
    ('all', ''), ('email', coalesce(p.email_domain, 'none')), ('device', p.device_name),
    ('os', p.client_os), ('browser', p.client_browser), ('screen', p.screen_resolution)
) AS dimensions(kind, key)
WHERE dimensions.key IS NOT NULL
GROUP BY dimensions.kind, dimensions.key;

COMMIT;
VACUUM (ANALYZE) purchases, decisions, review_resolutions, feature_counts;
