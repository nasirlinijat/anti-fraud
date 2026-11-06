CREATE TABLE rulesets (
    version          integer PRIMARY KEY,
    review_threshold integer NOT NULL CHECK (review_threshold > 0),
    block_threshold  integer NOT NULL,
    is_active        boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (review_threshold < block_threshold)
);

CREATE UNIQUE INDEX rulesets_one_active ON rulesets (is_active) WHERE is_active;

CREATE TABLE ruleset_rules (
    ruleset_version integer NOT NULL REFERENCES rulesets (version),
    rule_name       text    NOT NULL,
    enabled         boolean NOT NULL,
    weight          integer NOT NULL CHECK (weight >= 0),
    rule_limit      bigint  NOT NULL DEFAULT 0 CHECK (rule_limit >= 0),
    PRIMARY KEY (ruleset_version, rule_name)
);

INSERT INTO rulesets (version, review_threshold, block_threshold, is_active)
VALUES (1, 50, 80, true);

INSERT INTO ruleset_rules (ruleset_version, rule_name, enabled, weight, rule_limit) VALUES
    (1, 'high_amount',      true, 30, 50000),
    (1, 'card_velocity',    true, 40, 5),
    (1, 'new_account',      true, 20, 24),
    (1, 'country_mismatch', true, 25, 0);

CREATE FUNCTION reject_ruleset_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ruleset versions cannot be changed, create a new version instead';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER rulesets_immutable
    BEFORE UPDATE OF version, review_threshold, block_threshold, created_at OR DELETE ON rulesets
    FOR EACH ROW EXECUTE FUNCTION reject_ruleset_change();

CREATE TRIGGER ruleset_rules_immutable
    BEFORE UPDATE OR DELETE ON ruleset_rules
    FOR EACH ROW EXECUTE FUNCTION reject_ruleset_change();
