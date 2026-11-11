-- Existing versions keep the model in assist mode, as before. Their thresholds
-- stay empty, which means the thresholds from the loaded model's report.
ALTER TABLE rulesets
    ADD COLUMN model_mode text NOT NULL DEFAULT 'assist'
        CHECK (model_mode IN ('off', 'shadow', 'assist')),
    ADD COLUMN model_clear_below double precision,
    ADD COLUMN model_flag_at double precision,
    ADD CONSTRAINT rulesets_model_thresholds CHECK (
        (model_clear_below IS NULL AND model_flag_at IS NULL)
        OR (model_clear_below IS NOT NULL AND model_flag_at IS NOT NULL
            AND model_clear_below >= 0 AND model_flag_at <= 1 AND model_clear_below < model_flag_at)
    );

DROP TRIGGER rulesets_immutable ON rulesets;

CREATE TRIGGER rulesets_immutable
    BEFORE UPDATE OF version, review_threshold, block_threshold, note,
        model_mode, model_clear_below, model_flag_at, created_at
        OR DELETE ON rulesets
    FOR EACH ROW EXECUTE FUNCTION reject_ruleset_change();
