package platform

import (
	"crypto/subtle"
	"net/http"
)

// APIKeyHeader is the request header that carries the API key.
const APIKeyHeader = "X-API-Key"

// RequireAPIKey passes requests that carry the API key to next and rejects all
// others with 401. The key is compared in constant time.
func RequireAPIKey(key string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(APIKeyHeader)
		if key == "" || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			WriteError(w, http.StatusUnauthorized, "missing or invalid API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}
