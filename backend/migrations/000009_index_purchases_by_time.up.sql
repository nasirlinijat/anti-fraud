-- Serves the training export, which reads decisions in (event_time, transaction_id) order.
CREATE INDEX purchases_event_time ON purchases (event_time, transaction_id);
