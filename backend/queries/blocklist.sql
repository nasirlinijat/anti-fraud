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
INSERT INTO blocklist_entries (entry_type, value, reason)
VALUES (sqlc.arg(entry_type), sqlc.arg(value), sqlc.arg(reason))
RETURNING id, created_at;

-- name: DeleteBlocklistEntry :execrows
DELETE FROM blocklist_entries WHERE id = sqlc.arg(id);
