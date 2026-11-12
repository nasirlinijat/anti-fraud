package storage

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/redis/go-redis/v9"
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	rawURL := os.Getenv("TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	client, err := platform.NewRedis(rawURL)
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis ping failed: %v", err)
	}
	t.Cleanup(func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
	})
	_ = client.FlushDB(ctx).Err()
	return client
}

func TestCachedBlocklistStoreMatch(t *testing.T) {
	tx := blocklistTx(t)
	rClient := testRedis(t)
	dbStore := NewBlocklistStore(tx)
	cached := NewCachedBlocklistStore(dbStore, rClient)
	ctx := context.Background()

	if err := cached.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	tests := []struct {
		name    string
		subject models.BlocklistSubject
		want    *models.BlocklistEntry
	}{
		{"clean purchase misses", models.BlocklistSubject{CardToken: "good-card", PlayerID: "good-player"}, nil},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := cached.Match(ctx, tt.subject)
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

func TestCachedBlocklistStoreMatchLowestID(t *testing.T) {
	tx := blocklistTx(t)
	rClient := testRedis(t)
	dbStore := NewBlocklistStore(tx)
	cached := NewCachedBlocklistStore(dbStore, rClient)
	ctx := context.Background()

	if err := cached.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	got, ok, err := cached.Match(ctx, models.BlocklistSubject{
		CardToken: "bad-card",
		PlayerID:  "bad-player",
		DeviceID:  ptr("bad-device"),
	})
	if err != nil || !ok {
		t.Fatalf("Match = (%+v, %v, %v), want a match", got, ok, err)
	}
	if got.Type != models.BlocklistCard {
		t.Errorf("matched %s entry, want earliest entry (card)", got.Type)
	}
}

func TestCachedBlocklistStoreFallbackWhenNotReady(t *testing.T) {
	tx := blocklistTx(t)
	rClient := testRedis(t)
	dbStore := NewBlocklistStore(tx)
	cached := NewCachedBlocklistStore(dbStore, rClient)
	ctx := context.Background()

	// Cache has not been loaded, so ready key is missing.
	got, ok, err := cached.Match(ctx, models.BlocklistSubject{CardToken: "bad-card", PlayerID: "good-player"})
	if err != nil || !ok {
		t.Fatalf("Match = (%+v, %v, %v), want fallback to db match", got, ok, err)
	}
	if got.Type != models.BlocklistCard {
		t.Errorf("matched %s, want card", got.Type)
	}

	// Wait briefly for background reload to finish.
	time.Sleep(100 * time.Millisecond)
	if val := rClient.Get(ctx, keyReady).Val(); val != "1" {
		t.Errorf("ready key = %q after background reload, want 1", val)
	}
}

func TestCachedBlocklistStoreFallbackWhenRedisDown(t *testing.T) {
	tx := blocklistTx(t)
	dbStore := NewBlocklistStore(tx)
	ctx := context.Background()

	badClient, err := platform.NewRedis("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	defer badClient.Close()

	cached := NewCachedBlocklistStore(dbStore, badClient)
	got, ok, err := cached.Match(ctx, models.BlocklistSubject{CardToken: "bad-card", PlayerID: "good-player"})
	if err != nil {
		t.Fatalf("Match on down redis: %v", err)
	}
	if !ok || got.Type != models.BlocklistCard {
		t.Errorf("Match = (%+v, %v), want card match via db fallback", got, ok)
	}
}

func TestCachedBlocklistStoreAddAndRemove(t *testing.T) {
	tx := testTx(t)
	rClient := testRedis(t)
	dbStore := NewBlocklistStore(tx)
	cached := NewCachedBlocklistStore(dbStore, rClient)
	ctx := context.Background()

	if err := cached.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	saved, err := cached.Add(ctx, models.BlocklistEntry{
		Type:   models.BlocklistCard,
		Value:  "added-card",
		Reason: "fraud test",
	}, "op-1")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, ok, err := cached.Match(ctx, models.BlocklistSubject{CardToken: "added-card", PlayerID: "clean"})
	if err != nil || !ok || got.Value != "added-card" {
		t.Fatalf("Match after Add = (%+v, %v, %v)", got, ok, err)
	}

	if err := cached.Remove(ctx, saved.ID, "op-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	got, ok, err = cached.Match(ctx, models.BlocklistSubject{CardToken: "added-card", PlayerID: "clean"})
	if err != nil || ok {
		t.Fatalf("Match after Remove = (%+v, %v, %v), want no match", got, ok, err)
	}
}
