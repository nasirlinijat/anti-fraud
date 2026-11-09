package stats

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// Periods are the periods the dashboard can show, by name.
var Periods = map[string]time.Duration{
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"all": 0,
}

const (
	defaultPeriod = "24h"
	maxRules      = 10
)

// Source provides the statistics.
type Source interface {
	Summary(ctx context.Context, since time.Time, maxRules int) (Summary, error)
}

// Handler serves GET /stats.
type Handler struct {
	source Source
	now    func() time.Time
}

func NewHandler(source Source) *Handler {
	return &Handler{source: source, now: time.Now}
}

// RegisterRoutes registers GET /stats behind API key authentication.
func RegisterRoutes(mux *http.ServeMux, h *Handler, apiKey string) {
	mux.Handle("GET /stats", platform.RequireAPIKey(apiKey, h))
}

type response struct {
	Period      string    `json:"period"`
	Since       time.Time `json:"since"`
	GeneratedAt time.Time `json:"generated_at"`
	Summary
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = defaultPeriod
	}
	length, ok := Periods[period]
	if !ok {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{
			Error:  "invalid request",
			Fields: map[string]string{"period": "must be one of 15m, 1h, 24h, 7d, all"},
		})
		return
	}

	now := h.now().UTC().Truncate(time.Second)
	since := time.Unix(0, 0).UTC()
	if length > 0 {
		since = now.Add(-length)
	}

	sum, err := h.source.Summary(r.Context(), since, maxRules)
	if err != nil {
		log.Printf("stats for period %s: %v", period, err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	platform.WriteJSON(w, http.StatusOK, response{Period: period, Since: since, GeneratedAt: now, Summary: sum})
}
