DROP TRIGGER rulesets_immutable ON rulesets;

CREATE TRIGGER rulesets_immutable
    BEFORE UPDATE OF version, review_threshold, block_threshold, note, created_at OR DELETE ON rulesets
    FOR EACH ROW EXECUTE FUNCTION reject_ruleset_change();

ALTER TABLE rulesets
    DROP CONSTRAINT rulesets_model_thresholds,
    DROP COLUMN model_flag_at,
    DROP COLUMN model_clear_below,
    DROP COLUMN model_mode;
