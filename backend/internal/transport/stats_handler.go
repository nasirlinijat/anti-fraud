package transport

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats"
)

type StatsService interface {
	Window(period string, now time.Time) (models.StatsWindow, error)
	Summary(ctx context.Context, window models.StatsWindow) (models.StatsSummary, error)
}

// StatsHandler serves GET /stats.
type StatsHandler struct {
	service StatsService
	now     func() time.Time
}

func NewStatsHandler(service StatsService) *StatsHandler {
	return &StatsHandler{service: service, now: time.Now}
}

// RegisterStatsRoutes registers GET /stats behind API key authentication.
func RegisterStatsRoutes(mux *http.ServeMux, h *StatsHandler, apiKey string) {
	mux.Handle("GET /stats", platform.RequireAPIKey(apiKey, h))
}

type statsResponse struct {
	Period      string    `json:"period"`
	Since       time.Time `json:"since"`
	GeneratedAt time.Time `json:"generated_at"`
	models.StatsSummary
}

// ServeHTTP returns the dashboard numbers for the period in the query.
func (h *StatsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = stats.DefaultPeriod
	}
	now := h.now().UTC().Truncate(time.Microsecond)
	window, err := h.service.Window(period, now)
	if err != nil {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{
			Error:  "invalid request",
			Fields: map[string]string{"period": err.Error()},
		})
		return
	}

	sum, err := h.service.Summary(r.Context(), window)
	if err != nil {
		log.Printf("stats for period %s: %v", period, err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	platform.WriteJSON(w, http.StatusOK, statsResponse{Period: period, Since: window.Since, GeneratedAt: now, StatsSummary: sum})
}
