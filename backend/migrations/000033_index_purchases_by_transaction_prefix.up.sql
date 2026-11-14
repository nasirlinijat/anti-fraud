CREATE INDEX purchases_transaction_prefix ON purchases (transaction_id text_pattern_ops);
