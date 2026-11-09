-- Serve the per-player and per-card-issuer model features, which read the
-- player's or issuer's earlier purchases in (event_time, transaction_id) order.
CREATE INDEX purchases_player_event_time ON purchases (player_id, event_time, transaction_id);
CREATE INDEX purchases_issuer_event_time ON purchases ((coalesce(card_issuer, '')), event_time, transaction_id);

-- Running totals for the model features that count over all players: all
-- purchases (kind 'all', key ''), purchases per email domain (kind 'email',
-- missing domain is 'none') and per device name (kind 'device'). players is the
-- number of distinct players. Each saved purchase adds to its rows.
CREATE TABLE feature_counts (
    kind      text   NOT NULL CHECK (kind IN ('all', 'email', 'device')),
    key       text   NOT NULL,
    purchases bigint NOT NULL CHECK (purchases >= 0),
    players   bigint NOT NULL CHECK (players >= 0 AND players <= purchases),
    PRIMARY KEY (kind, key)
);

-- Start the totals from the purchases saved before this migration.
INSERT INTO feature_counts (kind, key, purchases, players)
SELECT 'all', '', count(*), count(DISTINCT player_id) FROM purchases HAVING count(*) > 0;

INSERT INTO feature_counts (kind, key, purchases, players)
SELECT 'email', coalesce(email_domain, 'none'), count(*), count(DISTINCT player_id)
FROM purchases
GROUP BY coalesce(email_domain, 'none');

INSERT INTO feature_counts (kind, key, purchases, players)
SELECT 'device', device_name, count(*), count(DISTINCT player_id)
FROM purchases
WHERE device_name IS NOT NULL
GROUP BY device_name;
