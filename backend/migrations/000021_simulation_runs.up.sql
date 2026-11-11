CREATE TABLE simulation_runs (
    id text PRIMARY KEY,
    operator_id text NOT NULL,
    request_id text NOT NULL,
    settings jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('starting','running','stopping','completed','stopped','failed','interrupted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    sent bigint NOT NULL DEFAULT 0 CHECK (sent >= 0),
    failed bigint NOT NULL DEFAULT 0 CHECK (failed >= 0),
    allowed bigint NOT NULL DEFAULT 0 CHECK (allowed >= 0),
    reviewed bigint NOT NULL DEFAULT 0 CHECK (reviewed >= 0),
    blocked bigint NOT NULL DEFAULT 0 CHECK (blocked >= 0),
    pending_transaction_id text,
    stop_requested_by text,
    error text NOT NULL DEFAULT '',
    UNIQUE (operator_id, request_id),
    CHECK (sent = allowed + reviewed + blocked)
);
CREATE UNIQUE INDEX simulation_runs_one_active ON simulation_runs ((true))
WHERE status IN ('starting','running','stopping');
CREATE INDEX simulation_runs_history ON simulation_runs (created_at DESC, id DESC);
