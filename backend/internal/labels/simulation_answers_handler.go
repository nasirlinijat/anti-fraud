package labels

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// SimulationAnswerReader reads private simulator comparisons.
type SimulationAnswerReader interface {
	Page(context.Context, SimulationAnswerQuery) (SimulationAnswerPage, error)
}

// RegisterSimulationAnswerRoutes serves comparison rows to signed-in admins.
func RegisterSimulationAnswerRoutes(mux *http.ServeMux, reader SimulationAnswerReader, enabled bool, access platform.Access) {
	if enabled {
		mux.Handle("GET /simulations/{id}/answers", access.ConfigRead(simulationAnswers(reader)))
	}
}

func simulationAnswers(reader SimulationAnswerReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q, valid := simulationAnswerQuery(r.PathValue("id"), r.URL.RawQuery)
		if !valid {
			platform.WriteError(w, http.StatusBadRequest, "invalid simulator answer query")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		page, err := reader.Page(ctx, q)
		if errors.Is(err, models.ErrSimulationNotFound) {
			platform.WriteError(w, http.StatusNotFound, "simulation run not found")
			return
		}
		if err != nil {
			slog.WarnContext(r.Context(), "simulation answers unavailable")
			platform.WriteError(w, http.StatusServiceUnavailable, "simulation answers are unavailable")
			return
		}
		platform.WriteJSON(w, http.StatusOK, page)
	}
}

func simulationAnswerQuery(runID, rawQuery string) (SimulationAnswerQuery, bool) {
	q := SimulationAnswerQuery{RunID: runID, Limit: 20}
	params, err := url.ParseQuery(rawQuery)
	if err != nil || runID == "" || len(runID) > 256 {
		return q, false
	}
	for key, values := range params {
		if len(values) != 1 || !applyAnswerParameter(&q, key, values[0]) {
			return q, false
		}
	}
	return q, true
}

func applyAnswerParameter(q *SimulationAnswerQuery, key, value string) bool {
	switch key {
	case "before":
		id, err := strconv.ParseInt(value, 10, 64)
		q.Before = id
		return err == nil && id > 0
	case "limit":
		limit, err := strconv.Atoi(value)
		q.Limit = limit
		return err == nil && limit >= 1 && limit <= 50
	case "filter":
		q.Exceptions = value == "exceptions"
		return q.Exceptions
	default:
		return false
	}
}
