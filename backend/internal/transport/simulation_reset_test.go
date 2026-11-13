package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
	"go.uber.org/mock/gomock"
)

func TestSimulationResetResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"reset", nil, 204}, {"busy", &simulation.ActiveError{}, 409},
		{"unavailable", errors.New("private database details"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux, service := simulationMux(t)
			service.EXPECT().Reset(gomock.Any(), "op-1").Return(tc.err)
			wantResponse(t, send(mux, "POST", "/simulations/reset", mockOperatorKey, ""), tc.status)
		})
	}
}

func TestSimulationResetAccess(t *testing.T) {
	mux, _ := simulationMux(t)
	for _, key := range []string{"", mockServiceKey, "wrong"} {
		wantResponse(t, send(mux, "POST", "/simulations/reset", key, ""), 401)
	}
	disabled := http.NewServeMux()
	RegisterSimulationRoutes(disabled, NewSimulationHandler(nil), false, platform.Access{})
	wantResponse(t, send(disabled, "POST", "/simulations/reset", "", ""), 404)
}

type resetSessions struct{ role string }

func (s resetSessions) Identify(context.Context, string) (platform.Identity, error) {
	return platform.Identity{Username: "admin", Role: s.role}, nil
}

func TestSimulationResetSessions(t *testing.T) {
	for _, role := range []string{platform.RoleAdmin, platform.RoleReviewer} {
		mux, service := simulationMux(t)
		mux = http.NewServeMux()
		RegisterSimulationRoutes(mux, NewSimulationHandler(service), true, platform.Access{Sessions: resetSessions{role}})
		status := 403
		if role == platform.RoleAdmin {
			status = 204
			service.EXPECT().Reset(gomock.Any(), "admin").Return(nil)
		}
		req := httptest.NewRequest("POST", "/simulations/reset", nil)
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		wantResponse(t, rec, status)
	}
}
