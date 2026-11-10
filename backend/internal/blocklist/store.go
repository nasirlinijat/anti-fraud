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
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
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
	q *db.Queries
}

func NewStore(conn db.DBTX) *Store {
	return &Store{q: db.New(conn)}
}

// Match reports whether the card, player, device, or IP address of the
// subject is on the blocklist, and returns the matching entry.
func (s *Store) Match(ctx context.Context, sub Subject) (Entry, bool, error) {
	row, err := s.q.FindBlocklistEntry(ctx, db.FindBlocklistEntryParams{
		CardToken: sub.CardToken,
		PlayerID:  sub.PlayerID,
		DeviceID:  optionalText(sub.DeviceID),
		IpAddress: ipText(sub.IPAddress),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("find blocklist entry: %w", err)
	}
	return Entry{Type: Type(row.EntryType), Value: row.Value, Reason: row.Reason}, true, nil
}

// List returns all entries, the newest first.
func (s *Store) List(ctx context.Context) ([]StoredEntry, error) {
	rows, err := s.q.ListBlocklistEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list blocklist entries: %w", err)
	}
	entries := make([]StoredEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, StoredEntry{
			ID:        r.ID,
			Type:      Type(r.EntryType),
			Value:     r.Value,
			Reason:    r.Reason,
			CreatedAt: r.CreatedAt.Time.UTC(),
		})
	}
	return entries, nil
}

// Add saves an entry. It returns ErrDuplicate if the same type and value are
// already on the blocklist. The entry applies to the next scored purchase.
func (s *Store) Add(ctx context.Context, e Entry) (StoredEntry, error) {
	row, err := s.q.InsertBlocklistEntry(ctx, db.InsertBlocklistEntryParams{
		EntryType: string(e.Type),
		Value:     e.Value,
		Reason:    e.Reason,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return StoredEntry{}, ErrDuplicate
		}
		return StoredEntry{}, fmt.Errorf("insert blocklist entry: %w", err)
	}
	return StoredEntry{ID: row.ID, Type: e.Type, Value: e.Value, Reason: e.Reason, CreatedAt: row.CreatedAt.Time.UTC()}, nil
}

// AddIfAbsent saves an entry unless the same type and value are already on the
// blocklist, and reports whether it saved one. Build the store on a
// transaction to make the insert part of that transaction.
func (s *Store) AddIfAbsent(ctx context.Context, e Entry) (StoredEntry, bool, error) {
	row, err := s.q.InsertBlocklistEntryIfAbsent(ctx, db.InsertBlocklistEntryIfAbsentParams{
		EntryType: string(e.Type),
		Value:     e.Value,
		Reason:    e.Reason,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredEntry{}, false, nil
	}
	if err != nil {
		return StoredEntry{}, false, fmt.Errorf("insert blocklist entry if absent: %w", err)
	}
	return StoredEntry{ID: row.ID, Type: e.Type, Value: e.Value, Reason: e.Reason, CreatedAt: row.CreatedAt.Time.UTC()}, true, nil
}

// Remove deletes the entry with the given ID. It returns ErrNotFound if there is none.
func (s *Store) Remove(ctx context.Context, id int64) error {
	n, err := s.q.DeleteBlocklistEntry(ctx, id)
	if err != nil {
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
