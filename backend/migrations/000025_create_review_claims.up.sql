-- A purchase under review has at most one claim, by the reviewer or admin who
-- is working on it. The claim stays after the decision as a record of who held
-- the purchase.
CREATE TABLE review_claims (
    purchase_id bigint      PRIMARY KEY REFERENCES decisions (purchase_id),
    account_id  bigint      NOT NULL REFERENCES accounts (id),
    claimed_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX review_claims_account ON review_claims (account_id, claimed_at);
