-- Blocked cards, devices, players, and IP addresses. A purchase that matches
-- any entry is blocked before the rules run. IP addresses are stored in the
-- canonical text form that Go's netip.Addr.String produces.
CREATE TABLE blocklist_entries (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entry_type text        NOT NULL CHECK (entry_type IN ('card', 'device', 'player', 'ip')),
    value      text        NOT NULL CHECK (value <> ''),
    reason     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Also the index for lookups by type and value.
    UNIQUE (entry_type, value)
);
