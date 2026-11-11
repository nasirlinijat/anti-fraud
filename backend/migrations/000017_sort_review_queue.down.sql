CREATE INDEX decisions_review_queue
    ON decisions (decided_at, purchase_id) WHERE outcome = 'review';

DROP INDEX decisions_review_queue_by_rule;
DROP INDEX decisions_review_queue_by_model;
