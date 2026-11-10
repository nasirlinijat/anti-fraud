-- name: ListModelInputs :many
-- Lists saved model inputs in (event_time, transaction_id) order, starting
-- after the given purchase, one page at a time.
SELECT p.transaction_id, p.event_time, p.player_id, d.model_inputs, d.model_score
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE (p.event_time, p.transaction_id) > (sqlc.arg(after_time)::timestamptz, sqlc.arg(after_id)::text)
ORDER BY p.event_time, p.transaction_id
LIMIT sqlc.arg(page_size);
