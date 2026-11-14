package transport

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

//go:generate mockgen -destination=mocks/mock_decision_status.go -package=mocks . DecisionStatusService

// DecisionStatusService reads minimal status for a service-authenticated caller.
type DecisionStatusService interface {
	Lookup(ctx context.Context, transactionID string) (models.DecisionStatus, error)
}

// DecisionStatusHandler serves minimal saved status to integrating game servers.
type DecisionStatusHandler struct {
	service DecisionStatusService
	limits  platform.ScoreLimits
}

func NewDecisionStatusHandler(service DecisionStatusService) *DecisionStatusHandler {
	return &DecisionStatusHandler{service: service}
}

// UseLimits applies the configured deadline and an independent polling allowance.
func (h *DecisionStatusHandler) UseLimits(limits platform.ScoreLimits) *DecisionStatusHandler {
	h.limits = limits
	return h
}

// RegisterDecisionStatusRoutes gives polling its own bucket, separate from scoring.
func RegisterDecisionStatusRoutes(mux *http.ServeMux, h *DecisionStatusHandler, apiKey string) {
	var handler http.Handler = h
	if h.limits.Timeout > 0 {
		handler = WithTimeout(h.limits.Timeout, handler)
	}
	if h.limits.RatePerSecond > 0 {
		handler = rateLimitMessage(newRateLimiter(h.limits.RatePerSecond, h.limits.Burst), handler, "too many status requests, retry later")
	}
	mux.Handle("GET /decision-status", platform.RequireAPIKey(apiKey, handler))
}

type decisionStatusResponse struct {
	TransactionID string     `json:"transaction_id"`
	Outcome       string     `json:"outcome"`
	ReviewStatus  string     `json:"review_status"`
	ResolvedAt    *time.Time `json:"resolved_at"`
}

func (h *DecisionStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, err := statusTransactionID(r.URL.RawQuery)
	if err != nil {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: map[string]string{"transaction_id": err.Error()}})
		return
	}
	result, err := h.service.Lookup(r.Context(), id)
	switch {
	case errors.Is(err, models.ErrPurchaseNotFound):
		platform.WriteError(w, http.StatusNotFound, "purchase not found")
	case err != nil && (r.Context().Err() != nil || errors.Is(err, context.DeadlineExceeded)):
		w.Header().Set("Retry-After", "1")
		platform.WriteError(w, http.StatusServiceUnavailable, "reading decision status took too long; retry later")
	case err != nil:
		platform.WriteServerError(r.Context(), w, "read decision status", err)
	default:
		platform.WriteJSON(w, http.StatusOK, decisionStatusResponse{result.TransactionID, string(result.Outcome), result.ReviewStatus, result.ResolvedAt})
	}
}

func statusTransactionID(rawQuery string) (string, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", errors.New("query must use valid URL encoding")
	}
	ids := values["transaction_id"]
	if len(ids) != 1 {
		return "", errors.New("must be supplied exactly once")
	}
	id := strings.TrimSpace(ids[0])
	if id == "" {
		return "", errors.New("is required")
	}
	if !utf8.ValidString(id) || strings.ContainsRune(id, 0) {
		return "", errors.New("must be valid text without null bytes")
	}
	return id, nil
}
