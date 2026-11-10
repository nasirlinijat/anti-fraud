-- name: ResolveReview :one
WITH candidate AS (
    SELECT d.purchase_id, d.outcome
    FROM decisions d
    WHERE d.purchase_id = sqlc.arg(target_purchase_id)
), saved AS (
    INSERT INTO review_resolutions (purchase_id, decision, analyst_id)
    SELECT candidate.purchase_id, sqlc.arg(decision)::text, sqlc.arg(analyst_id)::text
    FROM candidate
    WHERE candidate.outcome = 'review'
    ON CONFLICT (purchase_id) DO NOTHING
    RETURNING resolved_at
)
SELECT CASE
    WHEN EXISTS (SELECT 1 FROM saved) THEN 'saved'
    WHEN NOT EXISTS (SELECT 1 FROM candidate) THEN 'not_found'
    WHEN EXISTS (SELECT 1 FROM candidate WHERE outcome = 'review') THEN 'already_resolved'
    ELSE 'not_review'
END::text AS status,
(SELECT resolved_at FROM saved)::timestamptz AS resolved_at;

-- name: GetPurchaseDeviceID :one
-- Returns the device ID of a purchase, or NULL when it carried none.
SELECT device_id FROM purchases WHERE id = sqlc.arg(purchase_id);
