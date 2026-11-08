package blocklist

import (
	"context"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testTx returns a transaction with one entry of each type. It is rolled back
// when the test ends.
func testTx(t *testing.T) pgx.Tx {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(ctx) })

	_, err = tx.Exec(ctx, `
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

func ptr[T any](v T) *T { return &v }

func TestMatch(t *testing.T) {
	tx := testTx(t)
	store := NewStore(tx)

	clean := Subject{CardToken: "good-card", PlayerID: "good-player", DeviceID: ptr("good-device")}
	tests := []struct {
		name    string
		subject Subject
		want    *Entry // nil means no match
	}{
		{"clean purchase misses", clean, nil},
		{"card match", Subject{CardToken: "bad-card", PlayerID: "good-player"},
			&Entry{TypeCard, "bad-card", "stolen card"}},
		{"device match", Subject{CardToken: "good-card", PlayerID: "good-player", DeviceID: ptr("bad-device")},
			&Entry{TypeDevice, "bad-device", "emulator farm"}},
		{"player match", Subject{CardToken: "good-card", PlayerID: "bad-player"},
			&Entry{TypePlayer, "bad-player", "chargebacks"}},
		{"IPv4 match", Subject{CardToken: "good-card", PlayerID: "good-player", IPAddress: ptr(netip.MustParseAddr("203.0.113.9"))},
			&Entry{TypeIP, "203.0.113.9", "proxy"}},
		{"IPv6 match", Subject{CardToken: "good-card", PlayerID: "good-player", IPAddress: ptr(netip.MustParseAddr("2001:0db8:0000::1"))},
			&Entry{TypeIP, "2001:db8::1", "proxy"}},
		{"IP miss", Subject{CardToken: "good-card", PlayerID: "good-player", IPAddress: ptr(netip.MustParseAddr("203.0.113.10"))}, nil},
		{"device miss", Subject{CardToken: "good-card", PlayerID: "good-player", DeviceID: ptr("other-device")}, nil},
		// An entry only blocks its own type: a card entry does not block a player with the same value.
		{"value of another type misses", Subject{CardToken: "good-card", PlayerID: "bad-card", DeviceID: ptr("bad-player")}, nil},
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
	tx := testTx(t)

	got, ok, err := NewStore(tx).Match(context.Background(), Subject{
		CardToken: "bad-card", PlayerID: "bad-player", DeviceID: ptr("bad-device"),
	})
	if err != nil || !ok {
		t.Fatalf("Match = (%+v, %v, %v), want a match", got, ok, err)
	}
	if got.Type != TypeCard {
		t.Errorf("matched %s entry, want the earliest entry (card)", got.Type)
	}
}

func TestEntryTypeAndValueAreUnique(t *testing.T) {
	tx := testTx(t)

	_, err := tx.Exec(context.Background(),
		`INSERT INTO blocklist_entries (entry_type, value, reason) VALUES ('card', 'bad-card', 'again')`)
	if err == nil {
		t.Error("inserted a second card entry with the same value, want a unique violation")
	}
}

func TestLookupUsesIndex(t *testing.T) {
	tx := testTx(t)
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
