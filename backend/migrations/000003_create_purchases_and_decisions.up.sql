-- A scored purchase, as received in the score request. Fraud labels are never stored here.
CREATE TABLE purchases (
    id                     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id         text        NOT NULL UNIQUE,
    event_time             timestamptz NOT NULL,
    amount_minor           bigint      NOT NULL CHECK (amount_minor > 0),
    player_id              text        NOT NULL,
    card_token             text        NOT NULL,
    card_issuer            text,
    card_network           text,
    card_type              text,
    card_country           text,
    card_category          text,
    product_code           text,
    billing_region         text,
    billing_country        text,
    billing_distance       double precision,
    email_domain           text,
    recipient_email_domain text,
    device_type            text,
    device_name            text,
    device_id              text,
    ip_address             inet,
    ip_country             text,
    account_created_at     timestamptz,
    received_at            timestamptz NOT NULL DEFAULT now()
);

-- The decision for a purchase. Each purchase has exactly one.
CREATE TABLE decisions (
    purchase_id     bigint      PRIMARY KEY REFERENCES purchases (id),
    ruleset_version integer     NOT NULL REFERENCES rulesets (version),
    outcome         text        NOT NULL CHECK (outcome IN ('allow', 'review', 'block')),
    rule_score      integer     NOT NULL CHECK (rule_score >= 0),
    fired_rules     jsonb       NOT NULL CHECK (jsonb_typeof(fired_rules) = 'array'),
    model_inputs    jsonb       NOT NULL CHECK (jsonb_typeof(model_inputs) = 'object'),
    model_score     double precision CHECK (model_score BETWEEN 0 AND 1),
    decided_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX decisions_review_queue ON decisions (decided_at, purchase_id) WHERE outcome = 'review';
