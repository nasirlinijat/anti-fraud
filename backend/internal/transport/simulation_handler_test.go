package transport

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
)

type fakeSimulations struct {
	run      models.SimulationRun
	created  bool
	err      error
	started  *models.SimulationSettings
	operator string
	limit    int
	before   string
}

func (f *fakeSimulations) Start(_ context.Context, operatorID string, s models.SimulationSettings) (models.SimulationRun, bool, error) {
	f.started, f.operator = &s, operatorID
	return f.run, f.created, f.err
}

func (f *fakeSimulations) Get(context.Context, string) (models.SimulationRun, error) {
	return f.run, f.err
}

func (f *fakeSimulations) List(_ context.Context, before string, limit int) ([]models.SimulationRun, string, error) {
	f.before, f.limit = before, limit
	return []models.SimulationRun{}, "", f.err
}

func (f *fakeSimulations) Stop(_ context.Context, _, operatorID string) (models.SimulationRun, error) {
	f.operator = operatorID
	return f.run, f.err
}

func simulationMux(t *testing.T, service SimulationService) *http.ServeMux {
	t.Helper()
	ops, err := platform.ParseOperatorKeys(`[{"operator_id":"op","key":"operator"}]`, "service", platform.AnalystKeys{})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterSimulationRoutes(mux, NewSimulationHandler(service), true, "service", ops)
	return mux
}

func callSimulations(mux *http.ServeMux, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if key != "" {
		r.Header.Set("X-API-Key", key)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestSimulationRoutesDisabled(t *testing.T) {
	mux := http.NewServeMux()
	RegisterSimulationRoutes(mux, NewSimulationHandler(nil), false, "key", platform.OperatorKeys{})
	for _, path := range []string{"/simulations", "/simulations/x", "/simulations/x/stop"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
}

func TestSimulationRoutesNeedTheRightKey(t *testing.T) {
	mux := simulationMux(t, &fakeSimulations{created: true})
	body := `{"request_id":"one","mode":"live","rate":50}`
	for _, key := range []string{"", "service", "bad"} {
		if w := callSimulations(mux, "POST", "/simulations", key, body); w.Code != 401 {
			t.Fatal(key, w.Code)
		}
		if w := callSimulations(mux, "POST", "/simulations/sim-1/stop", key, ""); w.Code != 401 {
			t.Fatal(key, w.Code)
		}
	}
	for _, key := range []string{"operator", "bad", ""} {
		if w := callSimulations(mux, "GET", "/simulations/sim-1", key, ""); w.Code != 401 {
			t.Fatal(key, w.Code)
		}
		if w := callSimulations(mux, "GET", "/simulations", key, ""); w.Code != 401 {
			t.Fatal(key, w.Code)
		}
	}
}

func TestSimulationStartDecodesOverTheDefaults(t *testing.T) {
	fake := &fakeSimulations{run: models.SimulationRun{ID: "sim-1"}, created: true}
	w := callSimulations(simulationMux(t, fake), "POST", "/simulations", "operator", `{"request_id":"one","mode":"live","rate":50}`)
	if w.Code != 202 || w.Header().Get("Location") != "/simulations/sim-1" {
		t.Fatal(w.Code, w.Header().Get("Location"), w.Body.String())
	}
	want := simulation.DefaultSettings()
	if fake.operator != "op" || fake.started.Rate != 50 || fake.started.Seed != want.Seed || len(fake.started.Scenarios) != len(want.Scenarios) {
		t.Fatalf("operator %q, settings %+v", fake.operator, fake.started)
	}
}

func TestSimulationStartRejectsBadBodies(t *testing.T) {
	fake := &fakeSimulations{}
	mux := simulationMux(t, fake)
	for _, body := range []string{`{`, `{"mode":"live","request_id":"bad","unknown":1}`, `{"mode":"live","request_id":"bad"} {}`} {
		if w := callSimulations(mux, "POST", "/simulations", "operator", body); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	if fake.started != nil {
		t.Fatal("a bad body reached the service")
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
			fake := &fakeSimulations{run: models.SimulationRun{ID: "sim-1"}, created: tt.created, err: tt.err}
			w := callSimulations(simulationMux(t, fake), "POST", "/simulations", "operator", `{"request_id":"one","mode":"live"}`)
			if w.Code != tt.want || !bytes.Contains(w.Body.Bytes(), []byte(tt.body)) {
				t.Fatalf("status %d, body %s; want %d with %s", w.Code, w.Body.String(), tt.want, tt.body)
			}
		})
	}
}

func TestSimulationReadListAndStop(t *testing.T) {
	notFound := &fakeSimulations{err: models.ErrSimulationNotFound}
	if w := callSimulations(simulationMux(t, notFound), "GET", "/simulations/unknown", "service", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := callSimulations(simulationMux(t, notFound), "POST", "/simulations/unknown/stop", "operator", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}

	fake := &fakeSimulations{}
	mux := simulationMux(t, fake)
	if w := callSimulations(mux, "GET", "/simulations", "service", ""); w.Code != 200 || fake.limit != 20 || !bytes.Contains(w.Body.Bytes(), []byte(`"available_scenarios"`)) {
		t.Fatal(w.Code, fake.limit, w.Body.String())
	}
	if w := callSimulations(mux, "GET", "/simulations?limit=1&before=sim-9", "service", ""); w.Code != 200 || fake.limit != 1 || fake.before != "sim-9" {
		t.Fatal(w.Code, fake.limit, fake.before)
	}
	for _, path := range []string{"/simulations?limit=0", "/simulations?limit=101", "/simulations?limit=x"} {
		if w := callSimulations(mux, "GET", path, "service", ""); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	badCursor := &fakeSimulations{err: models.ErrInvalidSimulationCursor}
	if w := callSimulations(simulationMux(t, badCursor), "GET", "/simulations?before=unknown", "service", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}

	stopping := &fakeSimulations{run: models.SimulationRun{ID: "sim-1", Status: "stopping"}}
	if w := callSimulations(simulationMux(t, stopping), "POST", "/simulations/sim-1/stop", "operator", ""); w.Code != 202 || stopping.operator != "op" {
		t.Fatal(w.Code, stopping.operator)
	}
	stopped := &fakeSimulations{run: models.SimulationRun{ID: "sim-1", Status: "stopped"}}
	if w := callSimulations(simulationMux(t, stopped), "POST", "/simulations/sim-1/stop", "operator", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
