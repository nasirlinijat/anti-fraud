CREATE TABLE review_resolutions (
    purchase_id bigint PRIMARY KEY REFERENCES decisions (purchase_id),
    decision    text NOT NULL CHECK (decision IN ('approve', 'reject')),
    analyst_id  text NOT NULL CHECK (btrim(analyst_id) <> ''),
    resolved_at timestamptz NOT NULL DEFAULT now()
);
