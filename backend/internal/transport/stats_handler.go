package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats"
)

type StatsService interface {
	Window(period string, now time.Time) (models.StatsWindow, error)
	Summary(ctx context.Context, window models.StatsWindow) (models.StatsSummary, error)
	Recent(ctx context.Context, after *int64) (models.RecentPipeline, error)
}

var errInvalidAfter = errors.New("must be a non-negative integer")

// StatsHandler serves GET /stats.
type StatsHandler struct {
	service StatsService
	now     func() time.Time
}

func NewStatsHandler(service StatsService) *StatsHandler {
	return &StatsHandler{service: service, now: time.Now}
}

// RegisterStatsRoutes registers GET /stats and GET /stats/pipeline/recent
// behind the configuration read access check.
func RegisterStatsRoutes(mux *http.ServeMux, h *StatsHandler, access platform.Access) {
	mux.Handle("GET /stats", access.ConfigRead(h))
	mux.Handle("GET /stats/pipeline/recent", access.ConfigRead(http.HandlerFunc(h.recent)))
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
		platform.WriteServerError(r.Context(), w, "read stats", err, "period", period)
		return
	}
	platform.WriteJSON(w, http.StatusOK, statsResponse{Period: period, Since: window.Since, GeneratedAt: now, StatsSummary: sum})
}

// recent returns the decision counts after the purchase ID in the query.
func (h *StatsHandler) recent(w http.ResponseWriter, r *http.Request) {
	after, err := parseAfter(r)
	if err != nil {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{
			Error:  "invalid request",
			Fields: map[string]string{"after": err.Error()},
		})
		return
	}
	recent, err := h.service.Recent(r.Context(), after)
	if err != nil {
		platform.WriteServerError(r.Context(), w, "read recent pipeline", err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, recent)
}

// parseAfter reads the optional after cursor, nil when the query has none.
func parseAfter(r *http.Request) (*int64, error) {
	query := r.URL.Query()
	if !query.Has("after") {
		return nil, nil
	}
	after, err := strconv.ParseInt(query.Get("after"), 10, 64)
	if err != nil || after < 0 {
		return nil, errInvalidAfter
	}
	return &after, nil
}
