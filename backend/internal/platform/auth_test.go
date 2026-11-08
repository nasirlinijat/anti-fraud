package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAPIKey(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name   string
		key    string
		header string
		send   bool
		want   int
	}{
		{"valid key", "secret", "secret", true, http.StatusNoContent},
		{"missing header", "secret", "", false, http.StatusUnauthorized},
		{"empty header", "secret", "", true, http.StatusUnauthorized},
		{"wrong key", "secret", "secreT", true, http.StatusUnauthorized},
		{"key prefix", "secret", "secre", true, http.StatusUnauthorized},
		{"no key configured", "", "", true, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/score", nil)
			if tt.send {
				req.Header.Set(APIKeyHeader, tt.header)
			}
			rec := httptest.NewRecorder()
			RequireAPIKey(tt.key, next).ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.want == http.StatusUnauthorized && rec.Header().Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
			}
		})
	}
}
