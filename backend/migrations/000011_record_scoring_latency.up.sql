-- How long scoring took, in microseconds, from the start of the score request
-- until the decision was ready to save. Decisions saved before this column
-- existed leave it empty.
ALTER TABLE decisions
    ADD COLUMN scoring_latency_us bigint
    CHECK (scoring_latency_us >= 0);
