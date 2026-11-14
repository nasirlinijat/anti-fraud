package platform

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

// APIKeyHeader is the request header that carries the API key.
const APIKeyHeader = "X-API-Key"

// RequireAPIKey passes requests that carry a single X-API-Key header matching
// key to next and rejects all others with 401. Keys are hashed with SHA-256 and
// compared in constant time so key length is not leaked.
func RequireAPIKey(key string, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(key))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		values := r.Header.Values(APIKeyHeader)
		if key == "" || len(values) != 1 || values[0] == "" {
			WriteError(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		got := sha256.Sum256([]byte(values[0]))
		if subtle.ConstantTimeCompare(got[:], expected[:]) != 1 {
			WriteError(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}
