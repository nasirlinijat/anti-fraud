package storage

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

func TestBlocklistFilteredPagesNeverTruncateTheRedisReload(t *testing.T) {
	store, tx := blocklistPageStore(t)
	client := testRedis(t)
	_, err := tx.Exec(t.Context(), `INSERT INTO blocklist_entries(entry_type,value,reason)
		SELECT kind, CASE WHEN kind='ip' THEN '203.0.113.'||n ELSE kind||'-'||n END, 'reload regression'
		FROM unnest(ARRAY['card','device','player','ip']) kind CROSS JOIN generate_series(1,130) n`)
	if err != nil {
		t.Fatal(err)
	}
	cached := NewCachedBlocklistStore(store, client)
	page, err := cached.ListPage(t.Context(), models.BlocklistQuery{Type: "device", Query: "device", Page: 2, PageSize: 25})
	if err != nil || len(page.Entries) != 25 || page.Pagination.TotalItems != 130 {
		t.Fatalf("page %+v, err %v", page, err)
	}
	if err := cached.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.FlushDB(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cached.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, kind := range models.BlocklistTypes {
		if count := client.HLen(t.Context(), keyFor(kind)).Val(); count != 130 {
			t.Fatalf("%s mirror has %d entries, want 130", kind, count)
		}
	}
	for _, sub := range []models.BlocklistSubject{
		{CardToken: "card-1"}, {PlayerID: "player-130"}, {DeviceID: ptr("device-1")},
		{IPAddress: ptr(netip.MustParseAddr("203.0.113.130"))},
	} {
		if _, found, err := cached.Match(t.Context(), sub); err != nil || !found {
			t.Fatalf("entry outside filtered page did not match: %s", fmt.Sprint(found, err))
		}
	}
	all, err := cached.List(t.Context())
	if err != nil || len(all) != 520 {
		t.Fatalf("complete list length %d, err %v", len(all), err)
	}
}
