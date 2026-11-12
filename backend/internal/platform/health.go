package platform

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

const readyTimeout = 2 * time.Second

// Pinger checks whether a dependency is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// RegisterHealthRoutes registers GET /health and GET /ready. GET /health reports
// that the process is running. GET /ready reports whether the database is reachable.
func RegisterHealthRoutes(mux *http.ServeMux, db Pinger) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w, http.StatusOK, "ok")
	})

	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			slog.ErrorContext(r.Context(), "readiness check failed", "error", err)
			writeStatus(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		writeStatus(w, http.StatusOK, "ready")
	})
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"status":"` + status + `"}`))
}
