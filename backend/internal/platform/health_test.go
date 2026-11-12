package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func get(t *testing.T, db Pinger, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	RegisterHealthRoutes(mux, db)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthIgnoresDatabase(t *testing.T) {
	rec := get(t, fakePinger{err: errors.New("down")}, "/health")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestReadyWhenDatabaseReachable(t *testing.T) {
	rec := get(t, fakePinger{}, "/ready")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestReadyWhenDatabaseUnreachable(t *testing.T) {
	rec := get(t, fakePinger{err: errors.New("down")}, "/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if body := rec.Body.String(); body != `{"status":"unavailable"}` {
		t.Errorf("body = %s", body)
	}
}

func TestReadyWithUnreachablePool(t *testing.T) {
	pool, err := NewPool(context.Background(), "postgres://user:pass@127.0.0.1:1/none?connect_timeout=1", 0)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	rec := get(t, pool, "/ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestReadyWithPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := NewPool(context.Background(), url, 0)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	rec := get(t, pool, "/ready")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
