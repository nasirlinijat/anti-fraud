-- Usernames are stored in lowercase so that one person cannot hold two
-- accounts that differ only by case. The username is also saved as the
-- analyst or operator ID on decisions and audit events.
CREATE TABLE accounts (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      text        NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,63}$'),
    password_hash text        NOT NULL CHECK (password_hash <> ''),
    role          text        NOT NULL CHECK (role IN ('reviewer', 'admin')),
    disabled_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Only the SHA-256 hash of a session token is stored, so a database read
-- cannot be turned into a working session.
CREATE TABLE sessions (
    token_hash bytea       PRIMARY KEY CHECK (length(token_hash) = 32),
    account_id bigint      NOT NULL REFERENCES accounts (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at)
);
CREATE INDEX sessions_account ON sessions (account_id);
