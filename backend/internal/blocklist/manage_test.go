package blocklist_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/rulesets"
	"github.com/nasirlinijat/anti-fraud/backend/internal/score"
)

const testKey = "test-key"

func emptyTx(t *testing.T) pgx.Tx {
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
	if _, err := tx.Exec(ctx, "DELETE FROM blocklist_entries"); err != nil {
		t.Fatalf("empty blocklist: %v", err)
	}
	return tx
}

func TestAddListRemove(t *testing.T) {
	tx := emptyTx(t)
	ctx := context.Background()
	store := blocklist.NewStore(tx)

	card, err := store.Add(ctx, blocklist.Entry{Type: blocklist.TypeCard, Value: "card-1", Reason: "stolen"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := store.Add(ctx, blocklist.Entry{Type: blocklist.TypeIP, Value: "203.0.113.9", Reason: "proxy"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// The same value is allowed for another type.
	if _, err := store.Add(ctx, blocklist.Entry{Type: blocklist.TypePlayer, Value: "card-1", Reason: "same text"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	entries, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 || entries[2].ID != card.ID || entries[2].Reason != "stolen" || entries[2].CreatedAt.IsZero() {
		t.Fatalf("entries = %+v, want 3 with the card entry last", entries)
	}

	if err := store.Remove(ctx, card.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := store.Remove(ctx, card.ID); !errors.Is(err, blocklist.ErrNotFound) {
		t.Fatalf("second Remove = %v, want ErrNotFound", err)
	}
	if entries, _ := store.List(ctx); len(entries) != 2 {
		t.Fatalf("after remove %d entries, want 2", len(entries))
	}
}

func TestAddRejectsDuplicateInPostgres(t *testing.T) {
	tx := emptyTx(t)
	ctx := context.Background()
	store := blocklist.NewStore(tx)
	e := blocklist.Entry{Type: blocklist.TypeDevice, Value: "device-1", Reason: "emulator"}
	if _, err := store.Add(ctx, e); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// A failed insert aborts a Postgres transaction, so run it in a savepoint.
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Rollback(ctx)
	if _, err := blocklist.NewStore(sp).Add(ctx, e); !errors.Is(err, blocklist.ErrDuplicate) {
		t.Fatalf("second Add = %v, want ErrDuplicate", err)
	}
}

func TestAddIfAbsentSavesOnceAndKeepsTheFirstReason(t *testing.T) {
	tx := emptyTx(t)
	ctx := context.Background()
	store := blocklist.NewStore(tx)
	device := blocklist.Entry{Type: blocklist.TypeDevice, Value: "device-9", Reason: "first"}

	first, added, err := store.AddIfAbsent(ctx, device)
	if err != nil || !added || first.ID == 0 || first.Type != blocklist.TypeDevice || first.Value != "device-9" || first.CreatedAt.IsZero() {
		t.Fatalf("first AddIfAbsent = %+v, %v, %v; want a saved entry", first, added, err)
	}

	// No savepoint is needed: an existing entry is not an error, so the
	// transaction stays usable.
	device.Reason = "second"
	if _, added, err := store.AddIfAbsent(ctx, device); err != nil || added {
		t.Fatalf("second AddIfAbsent added = %v, err = %v; want no new entry and no error", added, err)
	}
	entries, err := store.List(ctx)
	if err != nil || len(entries) != 1 || entries[0].ID != first.ID || entries[0].Reason != "first" {
		t.Fatalf("entries = %+v, err = %v; want only the first entry", entries, err)
	}
	// The same value on another type is a different entry.
	if _, added, err := store.AddIfAbsent(ctx, blocklist.Entry{Type: blocklist.TypePlayer, Value: "device-9", Reason: "other type"}); err != nil || !added {
		t.Fatalf("other type: added = %v, err = %v; want saved", added, err)
	}
}

func TestAddIfAbsentIsPartOfItsTransaction(t *testing.T) {
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

	const value = "device-rolled-back"
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, added, err := blocklist.NewStore(tx).AddIfAbsent(ctx, blocklist.Entry{Type: blocklist.TypeDevice, Value: value, Reason: "test"}); err != nil || !added {
		t.Fatalf("AddIfAbsent added = %v, err = %v", added, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	_, found, err := blocklist.NewStore(pool).Match(ctx, blocklist.Subject{CardToken: "c", PlayerID: "p", DeviceID: ptrTo(value)})
	if err != nil || found {
		t.Fatalf("after rollback found = %v, err = %v; want the entry gone", found, err)
	}
}

func ptrTo[T any](v T) *T { return &v }

// TestNewEntryBlocksNextPurchase adds an entry through the API and scores a
// purchase right after it: the entry applies at once, with no restart or cache.
func TestNewEntryBlocksNextPurchase(t *testing.T) {
	tx := emptyTx(t)
	mux := http.NewServeMux()
	blocklist.RegisterRoutes(mux, blocklist.NewHandler(blocklist.NewStore(tx)), testKey)
	score.RegisterRoutes(mux, score.NewHandler(rulesets.NewStore(tx), blocklist.NewStore(tx), score.NewStore(tx)), testKey)

	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-API-Key", testKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	purchase := func(id string) string {
		return `{"transaction_id":"` + id + `","event_time":"2026-11-09T10:00:00Z","amount_minor":1999,"player_id":"p1","card_token":"c1","ip_address":"::ffff:198.51.100.4"}`
	}

	if rec := send(http.MethodPost, "/score", purchase("before")); !strings.Contains(rec.Body.String(), `"outcome":"allow"`) {
		t.Fatalf("before the entry: %s, want allow", rec.Body.String())
	}
	if rec := send(http.MethodPost, "/blocklist", `{"type":"ip","value":"198.51.100.4","reason":"proxy"}`); rec.Code != http.StatusCreated {
		t.Fatalf("add entry: status %d body %s", rec.Code, rec.Body.String())
	}
	if rec := send(http.MethodPost, "/score", purchase("after")); !strings.Contains(rec.Body.String(), `"outcome":"block"`) {
		t.Fatalf("after the entry: %s, want block", rec.Body.String())
	}
}
