package stats

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

var periods = map[string]struct{ length, bucketWidth time.Duration }{
	"15m": {15 * time.Minute, time.Minute},
	"1h":  {time.Hour, 5 * time.Minute},
	"24h": {24 * time.Hour, time.Hour},
	"7d":  {7 * 24 * time.Hour, 6 * time.Hour},
	"all": {},
}

const (
	defaultPeriod = "24h"
	maxRules      = 10
)

// Source provides the statistics.
type Source interface {
	Summary(ctx context.Context, window Window, maxRules int) (Summary, error)
}

// Window covers decisions from Since (inclusive) to Until (exclusive).
type Window struct {
	Since, Until time.Time
	// BucketWidth is zero when the store should choose a width for all time.
	BucketWidth time.Duration
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

// ServeHTTP returns the dashboard numbers for the period in the query.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = defaultPeriod
	}
	settings, ok := periods[period]
	if !ok {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{
			Error:  "invalid request",
			Fields: map[string]string{"period": "must be one of 15m, 1h, 24h, 7d, all"},
		})
		return
	}

	now := h.now().UTC().Truncate(time.Microsecond)
	since := time.Unix(0, 0).UTC()
	if settings.length > 0 {
		since = now.Add(-settings.length)
	}

	sum, err := h.source.Summary(r.Context(), Window{Since: since, Until: now, BucketWidth: settings.bucketWidth}, maxRules)
	if err != nil {
		log.Printf("stats for period %s: %v", period, err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	platform.WriteJSON(w, http.StatusOK, response{Period: period, Since: since, GeneratedAt: now, Summary: sum})
}
