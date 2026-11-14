//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

func TestBlocklistPagesUseRuntimePrivilegesAndAdminSessions(t *testing.T) {
	a := newApp(t)
	_, err := a.tx.Exec(t.Context(), `INSERT INTO blocklist_entries(entry_type,value,reason)
		SELECT 'device', 'af197-'||n, 'RING %_\' FROM generate_series(1,130) n`)
	if err != nil {
		t.Fatal(err)
	}
	path := "/blocklist?type=device&q=ring&page=2&page_size=25"
	page := expect[models.BlocklistPage](t, a.do(t, http.MethodGet, path, adminToken, ""), 200)
	if len(page.Entries) != 25 || page.Pagination.TotalItems != 130 || page.Pagination.Page != 2 {
		t.Fatalf("page %+v", page)
	}
	for _, credential := range []struct {
		key    string
		status int
	}{{reviewerToken, 403}, {serviceKey, 401}, {"", 401}} {
		if rec := a.do(t, http.MethodGet, path, credential.key, ""); rec.Code != credential.status {
			t.Fatalf("status %d, want %d", rec.Code, credential.status)
		}
	}
	legacy := expect[struct{ Entries []models.StoredBlocklistEntry }](t, a.do(t, http.MethodGet, "/blocklist", adminToken, ""), 200)
	if len(legacy.Entries) < 130 {
		t.Fatalf("legacy list lost entries: %d", len(legacy.Entries))
	}
}
