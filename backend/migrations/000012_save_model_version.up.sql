-- The version of the model that scored the decision, such as
-- lgbm-ieee-v5-combined. It is set whenever a model score is saved and empty
-- for rules-only decisions and decisions saved before this column existed.
ALTER TABLE decisions
    ADD COLUMN model_version text
    CHECK (model_version IS NULL OR (model_version <> '' AND model_score IS NOT NULL));
