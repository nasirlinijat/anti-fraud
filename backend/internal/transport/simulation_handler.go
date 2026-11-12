package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

const (
	maxSimulationBodyBytes   = 4096
	defaultSimulationHistory = 20
	maxSimulationHistory     = 100
)

type SimulationService interface {
	Start(ctx context.Context, operatorID string, settings models.SimulationSettings) (models.SimulationRun, bool, error)
	Get(ctx context.Context, id string) (models.SimulationRun, error)
	List(ctx context.Context, before string, limit int) ([]models.SimulationRun, string, error)
	Stop(ctx context.Context, id, operatorID string) (models.SimulationRun, error)
}

type SimulationHandler struct {
	service SimulationService
}

func NewSimulationHandler(service SimulationService) *SimulationHandler {
	return &SimulationHandler{service: service}
}

// RegisterSimulationRoutes registers the simulation routes: starting and
// stopping a run need an operator key, reading needs the service key. It
// registers nothing when simulations are disabled.
func RegisterSimulationRoutes(mux *http.ServeMux, h *SimulationHandler, enabled bool, access platform.Access) {
	if !enabled {
		return
	}
	mux.Handle("POST /simulations", bounded(access.ConfigWrite(h.start)))
	mux.Handle("POST /simulations/{id}/stop", bounded(access.ConfigWrite(h.stop)))
	mux.Handle("GET /simulations/{id}", bounded(access.ConfigRead(http.HandlerFunc(h.read))))
	mux.Handle("GET /simulations", bounded(access.ConfigRead(http.HandlerFunc(h.list))))
}

// bounded gives every simulation request five seconds to finish.
func bounded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func simulationFailure(w http.ResponseWriter, err error) {
	log.Printf("simulation operation: %v", err)
	platform.WriteError(w, http.StatusServiceUnavailable, "simulation service unavailable")
}

func (h *SimulationHandler) start(w http.ResponseWriter, r *http.Request, operatorID string) {
	settings, ok := readSimulationSettings(w, r)
	if !ok {
		return
	}
	run, created, err := h.service.Start(r.Context(), operatorID, settings)
	var invalid *simulation.ValidationError
	var active *simulation.ActiveError
	switch {
	case errors.As(err, &invalid):
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: invalid.Fields})
	case errors.Is(err, simulation.ErrShuttingDown):
		platform.WriteError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, simulation.ErrRequestReused):
		platform.WriteError(w, http.StatusConflict, err.Error())
	case errors.As(err, &active) && active.RunID == "":
		platform.WriteError(w, http.StatusConflict, "another simulation controller is active")
	case errors.As(err, &active):
		platform.WriteJSON(w, http.StatusConflict, struct {
			Error       string `json:"error"`
			ActiveRunID string `json:"active_run_id"`
		}{active.Error(), active.RunID})
	case err != nil:
		simulationFailure(w, err)
	case created:
		w.Header().Set("Location", "/simulations/"+run.ID)
		platform.WriteJSON(w, http.StatusAccepted, run)
	default:
		platform.WriteJSON(w, http.StatusOK, run)
	}
}

// readSimulationSettings decodes the settings of a new run over the defaults.
// On failure it writes the error response and returns false.
func readSimulationSettings(w http.ResponseWriter, r *http.Request) (models.SimulationSettings, bool) {
	s := simulation.DefaultSettings()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSimulationBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid simulation request")
		return models.SimulationSettings{}, false
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		platform.WriteError(w, http.StatusBadRequest, "expected one JSON object")
		return models.SimulationSettings{}, false
	}
	return s, true
}

func (h *SimulationHandler) read(w http.ResponseWriter, r *http.Request) {
	run, err := h.service.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, models.ErrSimulationNotFound) {
		platform.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		simulationFailure(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, run)
}

func (h *SimulationHandler) list(w http.ResponseWriter, r *http.Request) {
	limit := defaultSimulationHistory
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxSimulationHistory {
			platform.WriteError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	items, next, err := h.service.List(r.Context(), r.URL.Query().Get("before"), limit)
	if errors.Is(err, models.ErrInvalidSimulationCursor) {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		simulationFailure(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, struct {
		Items     []models.SimulationRun `json:"items"`
		Next      string                 `json:"next_cursor,omitempty"`
		Scenarios []simulator.Pattern    `json:"available_scenarios"`
	}{items, next, simulator.Patterns})
}

func (h *SimulationHandler) stop(w http.ResponseWriter, r *http.Request, operatorID string) {
	run, err := h.service.Stop(r.Context(), r.PathValue("id"), operatorID)
	if errors.Is(err, models.ErrSimulationNotFound) {
		platform.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		simulationFailure(w, err)
		return
	}
	status := http.StatusOK
	if run.Status == "stopping" {
		status = http.StatusAccepted
	}
	platform.WriteJSON(w, status, run)
}
