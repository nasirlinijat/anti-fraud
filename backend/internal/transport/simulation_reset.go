package transport

import (
	"errors"
	"net/http"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
)

func (h *SimulationHandler) reset(w http.ResponseWriter, r *http.Request, actorID string) {
	err := h.service.Reset(r.Context(), actorID)
	var active *simulation.ActiveError
	switch {
	case errors.As(err, &active):
		platform.WriteError(w, http.StatusConflict, "stop the active simulation before resetting")
	case err != nil:
		simulationFailure(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
