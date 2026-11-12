package storage

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// BlocklistStore reads and changes blocklist entries.
type BlocklistStore struct {
	conn platform.DBTX
}

func NewBlocklistStore(conn platform.DBTX) *BlocklistStore {
	return &BlocklistStore{conn: conn}
}

// findEntry returns one entry that matches the card, player, device, or IP
// address. A NULL device or IP address matches nothing.
const findEntry = `
SELECT entry_type, value, reason
FROM blocklist_entries
WHERE (entry_type = 'card'   AND value = $1)
   OR (entry_type = 'player' AND value = $2)
   OR (entry_type = 'device' AND value = $3)
   OR (entry_type = 'ip'     AND value = $4)
ORDER BY id
LIMIT 1`

// Match reports whether the card, player, device, or IP address of the
// subject is on the blocklist, and returns the matching entry.
func (s *BlocklistStore) Match(ctx context.Context, sub models.BlocklistSubject) (models.BlocklistEntry, bool, error) {
	var e models.BlocklistEntry
	err := s.conn.QueryRow(ctx, findEntry,
		sub.CardToken, sub.PlayerID, optionalText(sub.DeviceID), ipText(sub.IPAddress),
	).Scan(&e.Type, &e.Value, &e.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.BlocklistEntry{}, false, nil
	}
	if err != nil {
		return models.BlocklistEntry{}, false, fmt.Errorf("find blocklist entry: %w", err)
	}
	return e, true, nil
}

const listEntries = `
SELECT id, entry_type, value, reason, created_at
FROM blocklist_entries
ORDER BY created_at DESC, id DESC`

// List returns all entries, the newest first.
func (s *BlocklistStore) List(ctx context.Context) ([]models.StoredBlocklistEntry, error) {
	rows, err := s.conn.Query(ctx, listEntries)
	if err != nil {
		return nil, fmt.Errorf("list blocklist entries: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.StoredBlocklistEntry, error) {
		var e models.StoredBlocklistEntry
		err := row.Scan(&e.ID, &e.Type, &e.Value, &e.Reason, &e.CreatedAt)
		e.CreatedAt = e.CreatedAt.UTC()
		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("list blocklist entries: %w", err)
	}
	return entries, nil
}

// insertEntry adds an entry and its audit record in one statement.
const insertEntry = `
WITH added AS (
    INSERT INTO blocklist_entries (entry_type, value, reason)
    VALUES ($1, $2, $3)
    RETURNING id, entry_type, value, reason, created_at
), audit AS (
    INSERT INTO configuration_audit_events (actor_kind, actor_id, action, resource_id, details)
    SELECT 'operator', $4, 'blocklist.add', id,
           jsonb_build_object('type', entry_type, 'value', value, 'reason', reason)
    FROM added
)
SELECT id, created_at FROM added`

// Add saves an entry. It returns models.ErrBlocklistDuplicate if the same type and value are
// already on the blocklist. The entry applies to the next scored purchase.
func (s *BlocklistStore) Add(ctx context.Context, e models.BlocklistEntry, operatorID string) (models.StoredBlocklistEntry, error) {
	saved := models.StoredBlocklistEntry{Type: e.Type, Value: e.Value, Reason: e.Reason}
	err := s.conn.QueryRow(ctx, insertEntry, string(e.Type), e.Value, e.Reason, operatorID).Scan(&saved.ID, &saved.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return models.StoredBlocklistEntry{}, models.ErrBlocklistDuplicate
		}
		return models.StoredBlocklistEntry{}, fmt.Errorf("insert blocklist entry: %w", err)
	}
	saved.CreatedAt = saved.CreatedAt.UTC()
	return saved, nil
}

// insertEntryIfAbsent returns no row when the type and value are already on
// the blocklist.
const insertEntryIfAbsent = `
WITH added AS (
    INSERT INTO blocklist_entries (entry_type, value, reason)
    VALUES ($1, $2, $3)
    ON CONFLICT (entry_type, value) DO NOTHING
    RETURNING id, entry_type, value, reason, created_at
), audit AS (
    INSERT INTO configuration_audit_events (actor_kind, actor_id, action, resource_id, details)
    SELECT 'analyst', $4, 'blocklist.add', id,
           jsonb_build_object('type', entry_type, 'value', value, 'reason', reason)
    FROM added
)
SELECT id, created_at FROM added`

// AddIfAbsent saves an entry unless the same type and value are already on the
// blocklist, and reports whether it saved one. Build the store on a
// transaction to make the insert part of that transaction.
func (s *BlocklistStore) AddIfAbsent(ctx context.Context, e models.BlocklistEntry, analystID string) (models.StoredBlocklistEntry, bool, error) {
	saved := models.StoredBlocklistEntry{Type: e.Type, Value: e.Value, Reason: e.Reason}
	err := s.conn.QueryRow(ctx, insertEntryIfAbsent, string(e.Type), e.Value, e.Reason, analystID).Scan(&saved.ID, &saved.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.StoredBlocklistEntry{}, false, nil
	}
	if err != nil {
		return models.StoredBlocklistEntry{}, false, fmt.Errorf("insert blocklist entry if absent: %w", err)
	}
	saved.CreatedAt = saved.CreatedAt.UTC()
	return saved, true, nil
}

// deleteEntry removes an entry and records the removal. It returns the removed
// entry's type, value, and reason.
const deleteEntry = `
WITH removed AS (
    DELETE FROM blocklist_entries AS entry WHERE entry.id = $1
    RETURNING id, entry_type, value, reason
), audit AS (
    INSERT INTO configuration_audit_events (actor_kind, actor_id, action, resource_id, details)
    SELECT 'operator', $2, 'blocklist.remove', id,
           jsonb_build_object('type', entry_type, 'value', value, 'reason', reason)
    FROM removed
)
SELECT entry_type, value, reason FROM removed`

func (s *BlocklistStore) remove(ctx context.Context, id int64, operatorID string) (models.BlocklistEntry, error) {
	var e models.BlocklistEntry
	err := s.conn.QueryRow(ctx, deleteEntry, id, operatorID).Scan(&e.Type, &e.Value, &e.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.BlocklistEntry{}, models.ErrBlocklistEntryNotFound
	}
	if err != nil {
		return models.BlocklistEntry{}, fmt.Errorf("delete blocklist entry %d: %w", id, err)
	}
	return e, nil
}

// Remove deletes the entry with the given ID. It returns models.ErrBlocklistEntryNotFound if there is none.
func (s *BlocklistStore) Remove(ctx context.Context, id int64, operatorID string) error {
	_, err := s.remove(ctx, id, operatorID)
	return err
}

const findByTypeValue = `
SELECT id, reason
FROM blocklist_entries
WHERE entry_type = $1 AND value = $2
LIMIT 1`

// Find returns the id and reason for an entry of the given type and value.
// It reports whether the entry was found.
func (s *BlocklistStore) Find(ctx context.Context, t models.BlocklistType, value string) (int64, string, bool, error) {
	var id int64
	var reason string
	err := s.conn.QueryRow(ctx, findByTypeValue, string(t), value).Scan(&id, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("find blocklist entry %s/%s: %w", t, value, err)
	}
	return id, reason, true, nil
}

func optionalText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func ipText(addr *netip.Addr) pgtype.Text {
	if addr == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: addr.String(), Valid: true}
}
