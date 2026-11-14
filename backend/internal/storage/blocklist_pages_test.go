package storage

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

func blocklistPageStore(t *testing.T) (*BlocklistStore, pgx.Tx) {
	t.Helper()
	tx := testTx(t)
	if _, err := tx.Exec(t.Context(), `DELETE FROM blocklist_entries`); err != nil {
		t.Fatal(err)
	}
	return NewBlocklistStore(tx), tx
}

func TestBlocklistPageFiltersLiteralValuesAndReasons(t *testing.T) {
	store, tx := blocklistPageStore(t)
	_, err := tx.Exec(t.Context(), `INSERT INTO blocklist_entries (entry_type, value, reason) VALUES
		('card', 'Alpha%_\Token', 'Stolen Ring'), ('device', 'device-alpha', 'Confirmed RING'),
		('player', 'player-other', 'device ring'), ('ip', '203.0.113.9', 'Proxy'),
		('device', 'device-beta', 'literal.*[x]'), ('card', 'ÁBC', 'Unicode')`)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, query string
		kind        models.BlocklistType
		values      []string
	}{
		{"type", "", "device", []string{"device-beta", "device-alpha"}},
		{"text and case", "ALPHA", "", []string{"device-alpha", `Alpha%_\Token`}},
		{"reason", "ring", "", []string{"player-other", "device-alpha", `Alpha%_\Token`}},
		{"combined", "ring", "device", []string{"device-alpha"}},
		{"wildcards", `%_\`, "", []string{`Alpha%_\Token`}},
		{"percent", "%", "", []string{`Alpha%_\Token`}},
		{"underscore", "_", "", []string{`Alpha%_\Token`}},
		{"regex punctuation", ".*[x]", "", []string{"device-beta"}},
		{"unicode case", "ábc", "", []string{"ÁBC"}},
		{"no matches", "absent", "", nil},
		{"injection literal", "' OR true --", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := store.ListPage(t.Context(), models.BlocklistQuery{Type: tc.kind, Query: tc.query, Page: 1, PageSize: 25})
			if err != nil {
				t.Fatal(err)
			}
			var values []string
			for _, entry := range page.Entries {
				values = append(values, entry.Value)
			}
			if !reflect.DeepEqual(values, tc.values) || page.Pagination.TotalItems != int64(len(tc.values)) || page.Entries == nil {
				t.Fatalf("values %v, pagination %+v; want %v", values, page.Pagination, tc.values)
			}
		})
	}
}

func TestBlocklistPagesCountOrderAndClampAfterRemoval(t *testing.T) {
	store, tx := blocklistPageStore(t)
	_, err := tx.Exec(t.Context(), `INSERT INTO blocklist_entries (entry_type, value, reason, created_at)
		SELECT 'card', 'page-' || n, 'same timestamp',
		'2026-11-14'::timestamptz + CASE WHEN n = 1 THEN interval '1 day' ELSE interval '0' END
		FROM generate_series(1,55) n`)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for index, bounds := range [][2]string{{"page-1", "page-32"}, {"page-31", "page-7"}, {"page-6", "page-2"}} {
		page, err := store.ListPage(t.Context(), models.BlocklistQuery{Page: int64(index + 1), PageSize: 25})
		if err != nil || page.Pagination.TotalItems != 55 || page.Pagination.TotalPages != 3 {
			t.Fatalf("page %+v, err %v", page, err)
		}
		if page.Entries[0].Value != bounds[0] || page.Entries[len(page.Entries)-1].Value != bounds[1] {
			t.Fatalf("page %d has incorrect boundaries: %v", index+1, page.Entries)
		}
		for _, entry := range page.Entries {
			if seen[entry.ID] {
				t.Fatalf("entry %d appears twice", entry.ID)
			}
			seen[entry.ID] = true
		}
	}
	if len(seen) != 55 {
		t.Fatalf("saw %d entries", len(seen))
	}
	page, err := store.ListPage(t.Context(), models.BlocklistQuery{Page: math.MaxInt64, PageSize: 25})
	if err != nil || page.Pagination.Page != 3 || len(page.Entries) != 5 {
		t.Fatalf("clamped page %+v, err %v", page, err)
	}
	for _, entry := range page.Entries {
		if err := store.Remove(t.Context(), entry.ID, "page-test"); err != nil {
			t.Fatal(err)
		}
	}
	page, err = store.ListPage(t.Context(), models.BlocklistQuery{Page: 3, PageSize: 25})
	if err != nil || page.Pagination.Page != 2 || page.Pagination.TotalItems != 50 || len(page.Entries) != 25 {
		t.Fatalf("page after remove %+v, err %v", page, err)
	}
}

func TestBlocklistEmptyPageAndCompleteListStaySeparate(t *testing.T) {
	store, tx := blocklistPageStore(t)
	page, err := store.ListPage(context.Background(), models.BlocklistQuery{Page: 99, PageSize: 25})
	if err != nil || page.Entries == nil || page.Pagination != (models.BlocklistPagination{Page: 1, PageSize: 25, TotalPages: 1}) {
		t.Fatalf("empty page %+v, err %v", page, err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO blocklist_entries(entry_type,value,reason) SELECT 'card', 'full-'||n, 'r' FROM generate_series(1,130) n`); err != nil {
		t.Fatal(err)
	}
	page, err = store.ListPage(t.Context(), models.BlocklistQuery{Type: "device", Page: 1, PageSize: 1})
	all, allErr := store.List(t.Context())
	if err != nil || allErr != nil || page.Pagination.TotalItems != 0 || len(all) != 130 {
		t.Fatalf("page %+v, full list size %d, errors %v / %v", page, len(all), err, allErr)
	}
}
