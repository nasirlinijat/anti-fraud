CREATE TABLE configuration_audit_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_kind  text        NOT NULL CHECK (actor_kind IN ('operator', 'analyst')),
    actor_id    text        NOT NULL CHECK (btrim(actor_id) <> ''),
    action      text        NOT NULL CHECK (action IN ('ruleset.publish', 'blocklist.add', 'blocklist.remove')),
    resource_id bigint      NOT NULL CHECK (resource_id > 0),
    details     jsonb       NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(details) = 'object'),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
