package storage

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

func TestBlocklistOrderingIndexesSupportUnfilteredAndTypedPages(t *testing.T) {
	_, tx := blocklistPageStore(t)
	_, err := tx.Exec(t.Context(), `INSERT INTO blocklist_entries(entry_type,value,reason,created_at)
		SELECT (ARRAY['card','device','player','ip'])[1+n%4], 'index-'||n, 'r', now()-n*interval '1 second'
		FROM generate_series(1,4000) n`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `ANALYZE blocklist_entries`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind  models.BlocklistType
		index string
	}{{"", "blocklist_entries_newest_idx"}, {"device", "blocklist_entries_type_newest_idx"}} {
		filter := blocklistQueryFilter(models.BlocklistQuery{Type: tc.kind})
		sql := `EXPLAIN ` + blocklistPageEntries + filter.where + fmt.Sprintf(blocklistPageOrder, len(filter.args)+1, len(filter.args)+2)
		rows, err := tx.Query(t.Context(), sql, append(filter.args, 25, 0)...)
		if err != nil {
			t.Fatal(err)
		}
		lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
		plan := strings.Join(lines, "\n")
		if err != nil || !strings.Contains(plan, tc.index) || strings.Contains(plan, "Sort") {
			t.Fatalf("expected ordered index %s, err %v, plan:\n%s", tc.index, err, plan)
		}
	}
}
