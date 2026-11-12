package transport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

func simulationMux(t *testing.T) (*http.ServeMux, *mocks.MockSimulationService) {
	t.Helper()
	service := mocks.NewMockSimulationService(gomock.NewController(t))
	mux := http.NewServeMux()
	RegisterSimulationRoutes(mux, NewSimulationHandler(service), true, platform.Access{ServiceKey: mockServiceKey, Operators: mockOperators(t)})
	return mux, service
}

func TestSimulationRoutesDisabled(t *testing.T) {
	mux := http.NewServeMux()
	RegisterSimulationRoutes(mux, NewSimulationHandler(nil), false, platform.Access{ServiceKey: "key"})
	for _, path := range []string{"/simulations", "/simulations/x", "/simulations/x/stop"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
}

func TestSimulationRoutesNeedTheRightKey(t *testing.T) {
	mux, _ := simulationMux(t)
	body := `{"request_id":"one","mode":"live","rate":50}`
	for _, key := range []string{"", mockServiceKey, "bad"} {
		wantResponse(t, send(mux, "POST", "/simulations", key, body), http.StatusUnauthorized)
		wantResponse(t, send(mux, "POST", "/simulations/sim-1/stop", key, ""), http.StatusUnauthorized)
	}
	for _, key := range []string{mockOperatorKey, "bad", ""} {
		wantResponse(t, send(mux, "GET", "/simulations/sim-1", key, ""), http.StatusUnauthorized)
		wantResponse(t, send(mux, "GET", "/simulations", key, ""), http.StatusUnauthorized)
	}
}

func TestSimulationStartDecodesOverTheDefaults(t *testing.T) {
	mux, service := simulationMux(t)
	want := simulation.DefaultSettings()
	want.RequestID, want.Mode, want.Rate = "one", "live", 50
	service.EXPECT().Start(gomock.Any(), "op-1", want).Return(models.SimulationRun{ID: "sim-1"}, true, nil)

	rec := send(mux, "POST", "/simulations", mockOperatorKey, `{"request_id":"one","mode":"live","rate":50}`)
	wantResponse(t, rec, http.StatusAccepted, `"id":"sim-1"`)
	if rec.Header().Get("Location") != "/simulations/sim-1" {
		t.Fatalf("Location = %q", rec.Header().Get("Location"))
	}
}

func TestSimulationStartRejectsBadBodies(t *testing.T) {
	mux, service := simulationMux(t)
	service.EXPECT().Start(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	for _, body := range []string{`{`, `{"mode":"live","request_id":"bad","unknown":1}`, `{"mode":"live","request_id":"bad"} {}`} {
		wantResponse(t, send(mux, "POST", "/simulations", mockOperatorKey, body), http.StatusBadRequest)
	}
}

func TestSimulationStartStatusCodes(t *testing.T) {
	tests := []struct {
		name    string
		created bool
		err     error
		want    int
		body    string
	}{
		{"retry of an earlier run", false, nil, 200, `"id":"sim-1"`},
		{"invalid settings", false, &simulation.ValidationError{Fields: map[string]string{"rate": "must be between 1 and 50"}}, 400, `"rate":"must be between 1 and 50"`},
		{"request id reused", false, simulation.ErrRequestReused, 409, "request_id was used with different settings"},
		{"another run active", false, &simulation.ActiveError{RunID: "sim-0"}, 409, `"active_run_id":"sim-0"`},
		{"another controller active", false, &simulation.ActiveError{}, 409, "another simulation controller is active"},
		{"shutting down", false, simulation.ErrShuttingDown, 503, "simulation service is shutting down"},
		{"database down", false, errors.New("connection refused"), 503, "simulation service unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, service := simulationMux(t)
			service.EXPECT().Start(gomock.Any(), "op-1", gomock.Any()).Return(models.SimulationRun{ID: "sim-1"}, tt.created, tt.err)
			wantResponse(t, send(mux, "POST", "/simulations", mockOperatorKey, `{"request_id":"one","mode":"live"}`), tt.want, tt.body)
		})
	}
}

func TestSimulationRead(t *testing.T) {
	mux, service := simulationMux(t)
	service.EXPECT().Get(gomock.Any(), "sim-1").Return(models.SimulationRun{ID: "sim-1", Status: "running"}, nil)
	service.EXPECT().Get(gomock.Any(), "unknown").Return(models.SimulationRun{}, models.ErrSimulationNotFound)
	wantResponse(t, send(mux, "GET", "/simulations/sim-1", mockServiceKey, ""), http.StatusOK, `"status":"running"`)
	wantResponse(t, send(mux, "GET", "/simulations/unknown", mockServiceKey, ""), http.StatusNotFound)
}

func TestSimulationList(t *testing.T) {
	mux, service := simulationMux(t)
	service.EXPECT().List(gomock.Any(), "", 20).Return([]models.SimulationRun{}, "sim-3", nil)
	service.EXPECT().List(gomock.Any(), "sim-9", 1).Return([]models.SimulationRun{}, "", nil)
	service.EXPECT().List(gomock.Any(), "unknown", 20).Return(nil, "", models.ErrInvalidSimulationCursor)

	wantResponse(t, send(mux, "GET", "/simulations", mockServiceKey, ""), http.StatusOK, `"next_cursor":"sim-3"`, `"available_scenarios"`)
	wantResponse(t, send(mux, "GET", "/simulations?limit=1&before=sim-9", mockServiceKey, ""), http.StatusOK)
	wantResponse(t, send(mux, "GET", "/simulations?before=unknown", mockServiceKey, ""), http.StatusBadRequest)
	for _, path := range []string{"/simulations?limit=0", "/simulations?limit=101", "/simulations?limit=x"} {
		wantResponse(t, send(mux, "GET", path, mockServiceKey, ""), http.StatusBadRequest)
	}
}

func TestSimulationStop(t *testing.T) {
	tests := []struct {
		status string
		err    error
		want   int
	}{
		{"stopping", nil, http.StatusAccepted},
		{"stopped", nil, http.StatusOK},
		{"", models.ErrSimulationNotFound, http.StatusNotFound},
		{"", errors.New("connection refused"), http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.want), func(t *testing.T) {
			mux, service := simulationMux(t)
			service.EXPECT().Stop(gomock.Any(), "sim-1", "op-1").Return(models.SimulationRun{ID: "sim-1", Status: tt.status}, tt.err)
			wantResponse(t, send(mux, "POST", "/simulations/sim-1/stop", mockOperatorKey, ""), tt.want)
		})
	}
}
