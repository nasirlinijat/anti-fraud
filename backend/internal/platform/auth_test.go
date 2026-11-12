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
		name    string
		key     string
		headers []string
		want    int
	}{
		{"valid key", "secret", []string{"secret"}, http.StatusNoContent},
		{"missing header", "secret", nil, http.StatusUnauthorized},
		{"empty header", "secret", []string{""}, http.StatusUnauthorized},
		{"wrong key", "secret", []string{"secreT"}, http.StatusUnauthorized},
		{"key prefix", "secret", []string{"secre"}, http.StatusUnauthorized},
		{"longer key", "secret", []string{"secret-with-extra-bytes"}, http.StatusUnauthorized},
		{"duplicate header with valid first", "secret", []string{"secret", "other"}, http.StatusUnauthorized},
		{"duplicate header with valid second", "secret", []string{"other", "secret"}, http.StatusUnauthorized},
		{"duplicate identical headers", "secret", []string{"secret", "secret"}, http.StatusUnauthorized},
		{"no key configured", "", []string{""}, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/score", nil)
			for _, h := range tt.headers {
				req.Header.Add(APIKeyHeader, h)
			}
			rec := httptest.NewRecorder()
			RequireAPIKey(tt.key, next).ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if tt.want == http.StatusUnauthorized && rec.Header().Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
			}
		})
	}
}
