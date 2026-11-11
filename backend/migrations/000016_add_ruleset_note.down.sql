DROP TRIGGER rulesets_immutable ON rulesets;

CREATE TRIGGER rulesets_immutable
    BEFORE UPDATE OF version, review_threshold, block_threshold, created_at OR DELETE ON rulesets
    FOR EACH ROW EXECUTE FUNCTION reject_ruleset_change();

ALTER TABLE rulesets DROP COLUMN note;
