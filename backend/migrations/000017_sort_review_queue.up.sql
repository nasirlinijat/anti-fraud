-- Negative scores put the highest first while keeping time and ID ascending.
-- A missing model score sorts after every valid score in [0, 1].
CREATE INDEX decisions_review_queue_by_rule
    ON decisions ((-rule_score), decided_at, purchase_id) WHERE outcome = 'review';
CREATE INDEX decisions_review_queue_by_model
    ON decisions ((COALESCE(-model_score, 1::double precision)), decided_at, purchase_id) WHERE outcome = 'review';

DROP INDEX decisions_review_queue;
