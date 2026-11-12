-- A chargeback the card network reported for a purchase, received through the
-- signed processor webhook. Chargebacks are the fraud labels a merchant really
-- gets, weeks after the purchase. They are used only to export training labels
-- and are never read by scoring, model inputs, rules or reviews.
CREATE TABLE chargebacks (
    chargeback_id text        PRIMARY KEY CHECK (btrim(chargeback_id) <> '' AND length(chargeback_id) <= 200),
    purchase_id   bigint      NOT NULL REFERENCES purchases (id) ON DELETE CASCADE,
    reason_code   text        NOT NULL CHECK (reason_code IN (
        'fraud_card_not_present', 'fraud_card_absent_other', 'not_recognised',
        'product_not_received', 'duplicate', 'other')),
    amount_minor  bigint      NOT NULL CHECK (amount_minor > 0),
    received_at   timestamptz NOT NULL,
    recorded_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX chargebacks_purchase ON chargebacks (purchase_id);
