package storage

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

// blocklistTx returns a test transaction with one entry of each type.
func blocklistTx(t *testing.T) pgx.Tx {
	t.Helper()
	tx := testTx(t)
	_, err := tx.Exec(context.Background(), `
		INSERT INTO blocklist_entries (entry_type, value, reason) VALUES
			('card', 'bad-card', 'stolen card'),
			('device', 'bad-device', 'emulator farm'),
			('player', 'bad-player', 'chargebacks'),
			('ip', '203.0.113.9', 'proxy'),
			('ip', '2001:db8::1', 'proxy')`)
	if err != nil {
		t.Fatalf("insert entries: %v", err)
	}
	return tx
}

func TestMatch(t *testing.T) {
	tx := blocklistTx(t)
	store := NewBlocklistStore(tx)

	clean := models.BlocklistSubject{CardToken: "good-card", PlayerID: "good-player", DeviceID: ptr("good-device")}
	tests := []struct {
		name    string
		subject models.BlocklistSubject
		want    *models.BlocklistEntry // nil means no match
	}{
		{"clean purchase misses", clean, nil},
		{"card match", models.BlocklistSubject{CardToken: "bad-card", PlayerID: "good-player"},
			&models.BlocklistEntry{Type: models.BlocklistCard, Value: "bad-card", Reason: "stolen card"}},
		{"device match", models.BlocklistSubject{CardToken: "good-card", PlayerID: "good-player", DeviceID: ptr("bad-device")},
			&models.BlocklistEntry{Type: models.BlocklistDevice, Value: "bad-device", Reason: "emulator farm"}},
		{"player match", models.BlocklistSubject{CardToken: "good-card", PlayerID: "bad-player"},
			&models.BlocklistEntry{Type: models.BlocklistPlayer, Value: "bad-player", Reason: "chargebacks"}},
		{"IPv4 match", models.BlocklistSubject{CardToken: "good-card", PlayerID: "good-player", IPAddress: ptr(netip.MustParseAddr("203.0.113.9"))},
			&models.BlocklistEntry{Type: models.BlocklistIP, Value: "203.0.113.9", Reason: "proxy"}},
		{"IPv6 match", models.BlocklistSubject{CardToken: "good-card", PlayerID: "good-player", IPAddress: ptr(netip.MustParseAddr("2001:0db8:0000::1"))},
			&models.BlocklistEntry{Type: models.BlocklistIP, Value: "2001:db8::1", Reason: "proxy"}},
		{"IP miss", models.BlocklistSubject{CardToken: "good-card", PlayerID: "good-player", IPAddress: ptr(netip.MustParseAddr("203.0.113.10"))}, nil},
		{"device miss", models.BlocklistSubject{CardToken: "good-card", PlayerID: "good-player", DeviceID: ptr("other-device")}, nil},
		// An entry only blocks its own type: a card entry does not block a player with the same value.
		{"value of another type misses", models.BlocklistSubject{CardToken: "good-card", PlayerID: "bad-card", DeviceID: ptr("bad-player")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := store.Match(context.Background(), tt.subject)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			if tt.want == nil {
				if ok {
					t.Fatalf("Match = %+v, want no match", got)
				}
				return
			}
			if !ok || got != *tt.want {
				t.Errorf("Match = (%+v, %v), want (%+v, true)", got, ok, *tt.want)
			}
		})
	}
}

func TestMatchReturnsFirstEntryWhenSeveralMatch(t *testing.T) {
	tx := blocklistTx(t)

	got, ok, err := NewBlocklistStore(tx).Match(context.Background(), models.BlocklistSubject{
		CardToken: "bad-card", PlayerID: "bad-player", DeviceID: ptr("bad-device"),
	})
	if err != nil || !ok {
		t.Fatalf("Match = (%+v, %v, %v), want a match", got, ok, err)
	}
	if got.Type != models.BlocklistCard {
		t.Errorf("matched %s entry, want the earliest entry (card)", got.Type)
	}
}

func TestEntryTypeAndValueAreUnique(t *testing.T) {
	tx := blocklistTx(t)

	_, err := tx.Exec(context.Background(),
		`INSERT INTO blocklist_entries (entry_type, value, reason) VALUES ('card', 'bad-card', 'again')`)
	if err == nil {
		t.Error("inserted a second card entry with the same value, want a unique violation")
	}
}

func TestLookupUsesIndex(t *testing.T) {
	tx := blocklistTx(t)
	ctx := context.Background()

	// With only a few rows the planner prefers a sequential scan, so turn it off
	// to see whether the index can serve the lookup.
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("SET: %v", err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN SELECT entry_type, value, reason FROM blocklist_entries
		WHERE (entry_type = 'card' AND value = 'x') OR (entry_type = 'ip' AND value = 'y')`)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer rows.Close()

	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		plan += line + "\n"
	}
	if !strings.Contains(plan, "Index") {
		t.Errorf("plan does not use an index:\n%s", plan)
	}
}
