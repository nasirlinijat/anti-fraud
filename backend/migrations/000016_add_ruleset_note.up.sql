ALTER TABLE rulesets
    ADD COLUMN note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500);

DROP TRIGGER rulesets_immutable ON rulesets;

CREATE TRIGGER rulesets_immutable
    BEFORE UPDATE OF version, review_threshold, block_threshold, note, created_at OR DELETE ON rulesets
    FOR EACH ROW EXECUTE FUNCTION reject_ruleset_change();
