package blocklist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "test-key"

type fakeEntries struct {
	entries []StoredEntry
	added   []Entry
	removed []int64
	err     error
	addErr  error
	rmErr   error
}

func (f *fakeEntries) List(context.Context) ([]StoredEntry, error) { return f.entries, f.err }

func (f *fakeEntries) Add(_ context.Context, e Entry) (StoredEntry, error) {
	if f.addErr != nil {
		return StoredEntry{}, f.addErr
	}
	f.added = append(f.added, e)
	return StoredEntry{ID: 7, Type: e.Type, Value: e.Value, Reason: e.Reason, CreatedAt: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}, nil
}

func (f *fakeEntries) Remove(_ context.Context, id int64) error {
	if f.rmErr != nil {
		return f.rmErr
	}
	f.removed = append(f.removed, id)
	return nil
}

func call(t *testing.T, f *fakeEntries, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHandler(f), testKey)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestListEntries(t *testing.T) {
	f := &fakeEntries{entries: []StoredEntry{{ID: 1, Type: TypeCard, Value: "bad-card", Reason: "stolen", CreatedAt: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}}}
	rec := call(t, f, http.MethodGet, "/blocklist", "", testKey)
	want := `{"entries":[{"id":1,"type":"card","value":"bad-card","reason":"stolen","created_at":"2026-11-09T12:00:00Z"}]}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("status %d body %s, want 200 %s", rec.Code, rec.Body.String(), want)
	}

	rec = call(t, &fakeEntries{entries: []StoredEntry{}}, http.MethodGet, "/blocklist", "", testKey)
	if strings.TrimSpace(rec.Body.String()) != `{"entries":[]}` {
		t.Errorf("empty list body = %s", rec.Body.String())
	}
}

func TestAddEntry(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		saved Entry
	}{
		{"card", `{"type":"card","value":" bad-card ","reason":" stolen card "}`, Entry{TypeCard, "bad-card", "stolen card"}},
		{"player", `{"type":"player","value":"p1","reason":"chargebacks"}`, Entry{TypePlayer, "p1", "chargebacks"}},
		{"device", `{"type":"device","value":"d1","reason":"emulator"}`, Entry{TypeDevice, "d1", "emulator"}},
		{"ipv4", `{"type":"ip","value":"203.0.113.9","reason":"proxy"}`, Entry{TypeIP, "203.0.113.9", "proxy"}},
		{"mapped ipv4", `{"type":"ip","value":"::ffff:203.0.113.9","reason":"proxy"}`, Entry{TypeIP, "203.0.113.9", "proxy"}},
		{"ipv6", `{"type":"ip","value":"2001:DB8::1","reason":"proxy"}`, Entry{TypeIP, "2001:db8::1", "proxy"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeEntries{}
			rec := call(t, f, http.MethodPost, "/blocklist", tt.body, testKey)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
			}
			if len(f.added) != 1 || f.added[0] != tt.saved {
				t.Errorf("saved %+v, want %+v", f.added, tt.saved)
			}
			var got StoredEntry
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ID != 7 || got.Value != tt.saved.Value {
				t.Errorf("response %s", rec.Body.String())
			}
		})
	}
}

func TestAddRejectsInvalidEntries(t *testing.T) {
	long := strings.Repeat("x", 201)
	tests := []struct {
		name   string
		body   string
		fields map[string]string
	}{
		{"unknown type", `{"type":"email","value":"a","reason":"r"}`, map[string]string{"type": "must be one of card, device, player, ip"}},
		{"empty value", `{"type":"card","value":"  ","reason":"r"}`, map[string]string{"value": "is required"}},
		{"long value", `{"type":"card","value":"` + long + `","reason":"r"}`, map[string]string{"value": "must be at most 200 characters"}},
		{"bad ip", `{"type":"ip","value":"999.1.1.1","reason":"r"}`, map[string]string{"value": "must be an IPv4 or IPv6 address"}},
		{"empty reason", `{"type":"card","value":"a","reason":""}`, map[string]string{"reason": "is required"}},
		{"everything", `{}`, map[string]string{"type": "must be one of card, device, player, ip", "value": "is required", "reason": "is required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeEntries{}
			rec := call(t, f, http.MethodPost, "/blocklist", tt.body, testKey)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			var got struct{ Fields map[string]string }
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Fields) != len(tt.fields) {
				t.Fatalf("fields = %v, want %v", got.Fields, tt.fields)
			}
			for k, v := range tt.fields {
				if got.Fields[k] != v {
					t.Errorf("field %s = %q, want %q", k, got.Fields[k], v)
				}
			}
			if len(f.added) != 0 {
				t.Error("an invalid entry was saved")
			}
		})
	}

	for _, body := range []string{
		`not json`,
		`{"type":"card","value":"a","reason":"r","extra":1}`,
		`{"type":"card","value":"a","reason":"r"}{}`,
		`{"type":"card","value":"a","reason":"r"}null`,
	} {
		f := &fakeEntries{}
		if rec := call(t, f, http.MethodPost, "/blocklist", body, testKey); rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, rec.Code)
		}
		if len(f.added) != 0 {
			t.Error("an invalid entry was saved")
		}
	}
	oversized := `{"type":"card","value":"a","reason":"` + strings.Repeat("r", maxBodyBytes) + `"}`
	if rec := call(t, &fakeEntries{}, http.MethodPost, "/blocklist", oversized, testKey); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: status = %d, want 413", rec.Code)
	}
}

func TestAddRejectsDuplicate(t *testing.T) {
	rec := call(t, &fakeEntries{addErr: ErrDuplicate}, http.MethodPost, "/blocklist", `{"type":"card","value":"a","reason":"r"}`, testKey)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"value":"is already on the blocklist"`) {
		t.Fatalf("status %d body %s, want 409 naming value", rec.Code, rec.Body.String())
	}
}

func TestRemoveEntry(t *testing.T) {
	f := &fakeEntries{}
	if rec := call(t, f, http.MethodDelete, "/blocklist/12", "", testKey); rec.Code != http.StatusNoContent || len(f.removed) != 1 || f.removed[0] != 12 {
		t.Fatalf("status %d removed %v, want 204 and entry 12", rec.Code, f.removed)
	}
	if rec := call(t, &fakeEntries{rmErr: ErrNotFound}, http.MethodDelete, "/blocklist/12", "", testKey); rec.Code != http.StatusNotFound {
		t.Errorf("missing entry: status = %d, want 404", rec.Code)
	}
	for _, id := range []string{"abc", "0", "-3"} {
		if rec := call(t, &fakeEntries{}, http.MethodDelete, "/blocklist/"+id, "", testKey); rec.Code != http.StatusBadRequest {
			t.Errorf("id %s: status = %d, want 400", id, rec.Code)
		}
	}
}

func TestBlocklistRequiresAPIKey(t *testing.T) {
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/blocklist"},
		{http.MethodPost, "/blocklist"},
		{http.MethodDelete, "/blocklist/1"},
	} {
		if rec := call(t, &fakeEntries{}, r.method, r.path, `{}`, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", r.method, r.path, rec.Code)
		}
	}
}

func TestBlocklistInternalErrors(t *testing.T) {
	boom := errors.New("database is down")
	recs := []*httptest.ResponseRecorder{
		call(t, &fakeEntries{err: boom}, http.MethodGet, "/blocklist", "", testKey),
		call(t, &fakeEntries{addErr: boom}, http.MethodPost, "/blocklist", `{"type":"card","value":"a","reason":"r"}`, testKey),
		call(t, &fakeEntries{rmErr: boom}, http.MethodDelete, "/blocklist/1", "", testKey),
	}
	for _, rec := range recs {
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "database is down") {
			t.Errorf("status %d body %s, want 500 without the internal error", rec.Code, rec.Body.String())
		}
	}
}
