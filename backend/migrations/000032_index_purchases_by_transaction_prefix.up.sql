-- Serves the console list of a simulation run's blocked purchases, which finds
-- the run's purchases by the run ID that prefixes their transaction IDs. The
-- pattern operator class keeps the prefix range usable under any collation.
CREATE INDEX purchases_transaction_prefix ON purchases (transaction_id text_pattern_ops);
