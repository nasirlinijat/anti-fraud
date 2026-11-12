package models

import (
	"errors"
	"net/netip"
	"time"
)

// BlocklistType is the kind of value a blocklist entry blocks.
type BlocklistType string

const (
	BlocklistCard   BlocklistType = "card"
	BlocklistDevice BlocklistType = "device"
	BlocklistPlayer BlocklistType = "player"
	BlocklistIP     BlocklistType = "ip"
)

// BlocklistTypes lists every entry type.
var BlocklistTypes = []BlocklistType{BlocklistCard, BlocklistDevice, BlocklistPlayer, BlocklistIP}

// BlocklistEntry is a blocklist entry.
type BlocklistEntry struct {
	Type   BlocklistType
	Value  string
	Reason string
}

// StoredBlocklistEntry is a saved blocklist entry.
type StoredBlocklistEntry struct {
	ID        int64         `json:"id"`
	Type      BlocklistType `json:"type"`
	Value     string        `json:"value"`
	Reason    string        `json:"reason"`
	CreatedAt time.Time     `json:"created_at"`
}

// ErrBlocklistDuplicate means an entry with the same type and value already exists.
var ErrBlocklistDuplicate = errors.New("blocklist entry already exists")

// ErrBlocklistEntryNotFound means no entry has the given ID.
var ErrBlocklistEntryNotFound = errors.New("blocklist entry not found")

// BlocklistSubject holds the values of a purchase that are checked against the
// blocklist. DeviceID and IPAddress are nil when the purchase has none.
type BlocklistSubject struct {
	CardToken string
	PlayerID  string
	DeviceID  *string
	IPAddress *netip.Addr
}
