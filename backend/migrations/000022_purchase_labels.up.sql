CREATE TABLE purchase_labels (
 transaction_id text PRIMARY KEY REFERENCES purchases(transaction_id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK (run_id <> ''),
 source text NOT NULL CHECK (source IN ('simulator','ieee-cis')),
 scenario text,
 is_fraud boolean NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((source='simulator' AND scenario IS NOT NULL AND scenario<>'') OR (source='ieee-cis' AND scenario IS NULL))
);
CREATE INDEX purchase_labels_run ON purchase_labels (source,run_id);
-- Intent metadata is private and durable before dispatch. It is not an evaluated label.
CREATE TABLE pending_purchase_labels (
 transaction_id text PRIMARY KEY CHECK (transaction_id <> ''),
 run_id text NOT NULL CHECK (run_id <> ''),
 source text NOT NULL CHECK (source IN ('simulator','ieee-cis')),
 scenario text,
 is_fraud boolean NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((source='simulator' AND scenario IS NOT NULL AND scenario<>'') OR (source='ieee-cis' AND scenario IS NULL))
);
CREATE INDEX pending_purchase_labels_run ON pending_purchase_labels (run_id);
