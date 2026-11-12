-- Login and password-reset events that the game's account system reports for
-- a player. They feed the account takeover rules and the review case, never
-- the model inputs. A stored event never changes.
CREATE TABLE account_events (
    event_id    text        PRIMARY KEY CHECK (event_id <> ''),
    player_id   text        NOT NULL CHECK (player_id <> ''),
    event_type  text        NOT NULL CHECK (event_type IN ('login', 'password_reset')),
    event_time  timestamptz NOT NULL,
    device_id   text        CHECK (device_id <> ''),
    ip_country  text        CHECK (ip_country <> ''),
    received_at timestamptz NOT NULL DEFAULT now()
);

-- The review case and the takeover rules read a player's events in a window
-- before a purchase.
CREATE INDEX account_events_player_event_time ON account_events (player_id, event_time)
    INCLUDE (event_type, device_id, ip_country);

-- The latest password reset before a purchase is one index seek, however many
-- logins the player has.
CREATE INDEX account_events_password_reset ON account_events (player_id, event_time)
    WHERE event_type = 'password_reset';

-- Whether a player logged in from a device or a country before a given time
-- is one index seek, however long the player's history is.
CREATE INDEX account_events_login_device ON account_events (player_id, device_id, event_time)
    WHERE event_type = 'login' AND device_id IS NOT NULL;
CREATE INDEX account_events_login_country ON account_events (player_id, ip_country, event_time)
    WHERE event_type = 'login' AND ip_country IS NOT NULL;

-- The same seeks for the player's earlier purchases. The purchase ID leads to
-- its decision, which tells whether the purchase is trusted. Account events
-- store the IP country in upper case, and purchases keep it as sent, so
-- purchases are compared in upper case.
CREATE INDEX purchases_player_device_event_time ON purchases (player_id, device_id, event_time)
    INCLUDE (id) WHERE device_id IS NOT NULL;
CREATE INDEX purchases_player_country_event_time ON purchases (player_id, upper(ip_country), event_time)
    INCLUDE (id) WHERE ip_country IS NOT NULL;
