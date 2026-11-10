ALTER TABLE ruleset_rules
    ADD COLUMN window_days integer,
    ADD COLUMN min_amount_minor bigint,
    ADD COLUMN amount_multiplier integer,
    ADD CONSTRAINT ruleset_rules_new_device_spike_settings CHECK (
        CASE WHEN rule_name = 'new_device_spike' THEN
            rule_limit > 0
            AND window_days IS NOT NULL AND window_days BETWEEN 1 AND 3650
            AND min_amount_minor IS NOT NULL AND min_amount_minor > 0
            AND amount_multiplier IS NOT NULL AND amount_multiplier >= 2
        ELSE
            window_days IS NULL AND min_amount_minor IS NULL AND amount_multiplier IS NULL
        END
    );
