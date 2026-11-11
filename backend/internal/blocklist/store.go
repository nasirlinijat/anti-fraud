package blocklist

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// Type is the kind of value a blocklist entry blocks.
type Type string

const (
	TypeCard   Type = "card"
	TypeDevice Type = "device"
	TypePlayer Type = "player"
	TypeIP     Type = "ip"
)

// Types lists every entry type.
var Types = []Type{TypeCard, TypeDevice, TypePlayer, TypeIP}

// Entry is a blocklist entry.
type Entry struct {
	Type   Type
	Value  string
	Reason string
}

// StoredEntry is a saved blocklist entry.
type StoredEntry struct {
	ID        int64     `json:"id"`
	Type      Type      `json:"type"`
	Value     string    `json:"value"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrDuplicate means an entry with the same type and value already exists.
var ErrDuplicate = errors.New("blocklist entry already exists")

// ErrNotFound means no entry has the given ID.
var ErrNotFound = errors.New("blocklist entry not found")

// Subject holds the values of a purchase that are checked against the
// blocklist. DeviceID and IPAddress are nil when the purchase has none.
type Subject struct {
	CardToken string
	PlayerID  string
	DeviceID  *string
	IPAddress *netip.Addr
}

// Store reads blocklist entries from the database.
type Store struct {
	conn platform.DBTX
}

func NewStore(conn platform.DBTX) *Store {
	return &Store{conn: conn}
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
func (s *Store) Match(ctx context.Context, sub Subject) (Entry, bool, error) {
	var e Entry
	err := s.conn.QueryRow(ctx, findEntry,
		sub.CardToken, sub.PlayerID, optionalText(sub.DeviceID), ipText(sub.IPAddress),
	).Scan(&e.Type, &e.Value, &e.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("find blocklist entry: %w", err)
	}
	return e, true, nil
}

const listEntries = `
SELECT id, entry_type, value, reason, created_at
FROM blocklist_entries
ORDER BY created_at DESC, id DESC`

// List returns all entries, the newest first.
func (s *Store) List(ctx context.Context) ([]StoredEntry, error) {
	rows, err := s.conn.Query(ctx, listEntries)
	if err != nil {
		return nil, fmt.Errorf("list blocklist entries: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (StoredEntry, error) {
		var e StoredEntry
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

// Add saves an entry. It returns ErrDuplicate if the same type and value are
// already on the blocklist. The entry applies to the next scored purchase.
func (s *Store) Add(ctx context.Context, e Entry, operatorID string) (StoredEntry, error) {
	saved := StoredEntry{Type: e.Type, Value: e.Value, Reason: e.Reason}
	err := s.conn.QueryRow(ctx, insertEntry, string(e.Type), e.Value, e.Reason, operatorID).Scan(&saved.ID, &saved.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return StoredEntry{}, ErrDuplicate
		}
		return StoredEntry{}, fmt.Errorf("insert blocklist entry: %w", err)
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
func (s *Store) AddIfAbsent(ctx context.Context, e Entry, analystID string) (StoredEntry, bool, error) {
	saved := StoredEntry{Type: e.Type, Value: e.Value, Reason: e.Reason}
	err := s.conn.QueryRow(ctx, insertEntryIfAbsent, string(e.Type), e.Value, e.Reason, analystID).Scan(&saved.ID, &saved.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredEntry{}, false, nil
	}
	if err != nil {
		return StoredEntry{}, false, fmt.Errorf("insert blocklist entry if absent: %w", err)
	}
	saved.CreatedAt = saved.CreatedAt.UTC()
	return saved, true, nil
}

// deleteEntry removes an entry and records the removal. It returns the number
// of entries removed.
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
SELECT count(*) FROM removed`

// Remove deletes the entry with the given ID. It returns ErrNotFound if there is none.
func (s *Store) Remove(ctx context.Context, id int64, operatorID string) error {
	var n int64
	if err := s.conn.QueryRow(ctx, deleteEntry, id, operatorID).Scan(&n); err != nil {
		return fmt.Errorf("delete blocklist entry %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const uniqueViolation = "23505"

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
