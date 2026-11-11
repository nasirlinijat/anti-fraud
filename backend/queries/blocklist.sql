-- name: FindBlocklistEntry :one
-- Returns one entry that matches the card, player, device, or IP address. A
-- NULL device or IP address matches nothing.
SELECT entry_type, value, reason
FROM blocklist_entries
WHERE (entry_type = 'card'   AND value = sqlc.arg(card_token))
   OR (entry_type = 'player' AND value = sqlc.arg(player_id))
   OR (entry_type = 'device' AND value = sqlc.narg(device_id))
   OR (entry_type = 'ip'     AND value = sqlc.narg(ip_address))
ORDER BY id
LIMIT 1;

-- name: ListBlocklistEntries :many
-- Lists all entries, the newest first.
SELECT id, entry_type, value, reason, created_at
FROM blocklist_entries
ORDER BY created_at DESC, id DESC;

-- name: InsertBlocklistEntry :one
WITH added AS (
    INSERT INTO blocklist_entries (entry_type, value, reason)
    VALUES (sqlc.arg(entry_type), sqlc.arg(value), sqlc.arg(reason))
    RETURNING id, entry_type, value, reason, created_at
), audit AS (
    INSERT INTO configuration_audit_events (actor_kind, actor_id, action, resource_id, details)
    SELECT 'operator', sqlc.arg(operator_id), 'blocklist.add', id,
           jsonb_build_object('type', entry_type, 'value', value, 'reason', reason)
    FROM added
)
SELECT id, created_at FROM added;

-- name: InsertBlocklistEntryIfAbsent :one
-- Returns no row when the type and value are already on the blocklist.
WITH added AS (
    INSERT INTO blocklist_entries (entry_type, value, reason)
    VALUES (sqlc.arg(entry_type), sqlc.arg(value), sqlc.arg(reason))
    ON CONFLICT (entry_type, value) DO NOTHING
    RETURNING id, entry_type, value, reason, created_at
), audit AS (
    INSERT INTO configuration_audit_events (actor_kind, actor_id, action, resource_id, details)
    SELECT 'analyst', sqlc.arg(analyst_id), 'blocklist.add', id,
           jsonb_build_object('type', entry_type, 'value', value, 'reason', reason)
    FROM added
)
SELECT id, created_at FROM added;

-- name: DeleteBlocklistEntry :one
WITH removed AS (
    DELETE FROM blocklist_entries AS entry WHERE entry.id = sqlc.arg(entry_id)
    RETURNING id, entry_type, value, reason
), audit AS (
    INSERT INTO configuration_audit_events (actor_kind, actor_id, action, resource_id, details)
    SELECT 'operator', sqlc.arg(operator_id), 'blocklist.remove', id,
           jsonb_build_object('type', entry_type, 'value', value, 'reason', reason)
    FROM removed
)
SELECT count(*) FROM removed;
