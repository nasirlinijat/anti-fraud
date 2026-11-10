DO $$
BEGIN
    LOCK TABLE ruleset_rules IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM ruleset_rules WHERE rule_name = 'new_device_spike') THEN
        RAISE EXCEPTION 'cannot drop new device spike settings from published rulesets';
    END IF;

    ALTER TABLE ruleset_rules
        DROP CONSTRAINT ruleset_rules_new_device_spike_settings,
        DROP COLUMN amount_multiplier,
        DROP COLUMN min_amount_minor,
        DROP COLUMN window_days;
END
$$;
