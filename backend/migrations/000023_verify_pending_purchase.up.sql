ALTER TABLE pending_purchase_labels ADD COLUMN request_json jsonb;
ALTER TABLE pending_purchase_labels ADD COLUMN rejection_reason text;
-- Old intents lack a purchase snapshot and must never be automatically trusted.
UPDATE pending_purchase_labels SET rejection_reason='legacy intent has no purchase snapshot';
