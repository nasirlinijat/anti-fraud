package blocklist

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5"
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

// Entry is a blocklist entry.
type Entry struct {
	Type   Type
	Value  string
	Reason string
}

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
