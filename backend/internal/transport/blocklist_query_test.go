package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

func TestBlocklistPageParsesFiltersAndReturnsMetadata(t *testing.T) {
	for _, query := range []string{"page=2&page_size=10&type=device&q=" + url.QueryEscape(`  %_\.*  `), "type=card", "q=ring", "q=&type="} {
		f := &fakeEntries{entries: []models.StoredBlocklistEntry{}}
		rec := call(t, f, http.MethodGet, "/blocklist?"+query, "", blocklistAdminToken)
		if rec.Code != http.StatusOK || len(f.queries) != 1 {
			t.Fatalf("query %q: %d %s, reads %v", query, rec.Code, rec.Body, f.queries)
		}
		var page models.BlocklistPage
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.Pagination.PageSize == 0 || page.Entries == nil {
			t.Fatalf("page response: %s (%v)", rec.Body, err)
		}
		if strings.HasPrefix(query, "page=2") && f.queries[0] != (models.BlocklistQuery{Type: "device", Query: `%_\.*`, Page: 2, PageSize: 10}) {
			t.Fatalf("parsed query = %+v", f.queries[0])
		}
	}
}

func TestBlocklistPageRejectsInvalidQueriesBeforeReading(t *testing.T) {
	cases := []struct{ query, field string }{
		{"type=email", "type"}, {"type=Card", "type"}, {"type=card&type=ip", "type"},
		{"q=x&q=y", "q"}, {"q=%00", "q"}, {"q=%FF", "q"}, {"q=%0A", "q"},
		{"q=" + strings.Repeat("x", 201), "q"}, {"q=" + url.QueryEscape(strings.Repeat("😀", 201)), "q"},
		{"page=0", "page"}, {"page=-1", "page"}, {"page=", "page"}, {"page=1.5", "page"},
		{"page=%2B1", "page"}, {"page=9223372036854775808", "page"}, {"page=1&page=2", "page"},
		{"page_size=0", "page_size"}, {"page_size=101", "page_size"}, {"page_size=-1", "page_size"},
		{"page_size=1&page_size=2", "page_size"}, {"page_size=9223372036854775808", "page_size"},
		{"q=%ZZ", "query"}, {"q=x;y", "query"}, {"unknown=x", "query"},
		{"q=" + strings.Repeat("x", 4096), "query"},
	}
	for _, tc := range cases {
		t.Run(tc.query[:min(len(tc.query), 60)], func(t *testing.T) {
			f := &fakeEntries{}
			rec := call(t, f, http.MethodGet, "/blocklist?"+tc.query, "", blocklistAdminToken)
			var response platform.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != 400 || response.Fields[tc.field] == "" || len(f.queries) != 0 {
				t.Fatalf("status %d body %s, queries %v", rec.Code, rec.Body, f.queries)
			}
		})
	}
}

func TestBlocklistPagesAndWritesRequireAdminSessions(t *testing.T) {
	credentials := []struct {
		name, header, value string
		status              int
	}{
		{"missing", "", "", 401}, {"service key", "X-API-Key", blocklistServiceKey, 401},
		{"invalid session", "Authorization", "Bearer invalid", 401},
		{"reviewer", "Authorization", "Bearer mock-reviewer-token", 403},
	}
	for _, credential := range credentials {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			t.Run(credential.name+method, func(t *testing.T) {
				f := &fakeEntries{}
				mux := http.NewServeMux()
				RegisterBlocklistRoutes(mux, NewBlocklistHandler(blocklist.NewService(f)), platform.Access{Sessions: consoleTestSessions})
				path := "/blocklist?page=0"
				if method == http.MethodDelete {
					path = "/blocklist/1"
				}
				req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
				req.Header.Set(credential.header, credential.value)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != credential.status || len(f.queries)+len(f.added)+len(f.removed) != 0 {
					t.Fatalf("status %d, want %d; body %s", rec.Code, credential.status, rec.Body)
				}
			})
		}
	}
}

func TestBlocklistPageLogsNoSearchText(t *testing.T) {
	logs := captureLogs(t)
	mux := http.NewServeMux()
	RegisterBlocklistRoutes(mux, NewBlocklistHandler(blocklist.NewService(&fakeEntries{entries: []models.StoredBlocklistEntry{}})), platform.Access{Sessions: consoleTestSessions})
	marker := "private-af197-search-marker"
	req := httptest.NewRequest(http.MethodGet, "/blocklist?page=1&q="+marker, nil)
	req.Header.Set("Authorization", "Bearer "+blocklistAdminToken)
	rec := httptest.NewRecorder()
	Middleware(mux).ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(logs.String(), marker) {
		t.Fatal(fmt.Sprintf("status %d, search text logged: %t", rec.Code, strings.Contains(logs.String(), marker)))
	}
}

func TestBlocklistPageDatabaseOutageReturns503AndRetryAfter(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	mux := http.NewServeMux()
	RegisterBlocklistRoutes(mux, NewBlocklistHandler(blocklist.NewService(storage.NewBlocklistStore(pool))), platform.Access{Sessions: consoleTestSessions})
	rec := send(mux, http.MethodGet, "/blocklist?page=1", mockAdminToken, "")
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "1" || !strings.Contains(rec.Body.String(), platform.DatabaseUnavailableMessage) {
		t.Fatalf("status %d, Retry-After %q, body %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body)
	}
}

func TestBlocklistPageOtherErrorsStay500(t *testing.T) {
	rec := call(t, &fakeEntries{err: errors.New("private database error")}, http.MethodGet, "/blocklist?page=1", "", blocklistAdminToken)
	if rec.Code != 500 || rec.Header().Get("Retry-After") != "" || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}
