package transport

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"go.uber.org/mock/gomock"
)

func TestSimulationBlockedPurchases(t *testing.T) {
	mux, service := simulationMux(t)
	country := "NG"
	item := models.BlockedPurchase{
		PurchaseID: 42, TransactionID: "sim-1-txn-7", PlayerID: "sim-1-player-3", AmountMinor: 1999,
		EventTime: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), IPCountry: &country,
		Reason: models.BlockReasonRules, RuleScore: 95, BlockThreshold: 80, RulesetVersion: 2,
		FiredRules: []scoring.FiredRule{{Name: "velocity", Points: 95, Message: "7 purchases in 10 minutes"}},
	}
	service.EXPECT().BlockedPurchases(gomock.Any(), "sim-1", int64(0), 20).Return([]models.BlockedPurchase{item}, int64(42), nil)

	wantResponse(t, send(mux, "GET", "/simulations/sim-1/blocked", mockServiceKey, ""), http.StatusOK,
		`"purchase_id":"42"`, `"amount_minor":"1999"`, `"ip_country":"NG"`, `"card_country":null`,
		`"reason":"rules"`, `"block_threshold":80`, `"name":"velocity"`, `"next_cursor":"42"`)
}

func TestSimulationBlockedPurchasesPassesTheCursor(t *testing.T) {
	mux, service := simulationMux(t)
	service.EXPECT().BlockedPurchases(gomock.Any(), "sim-1", int64(42), 5).Return(nil, int64(0), nil)

	rec := send(mux, "GET", "/simulations/sim-1/blocked?before=42&limit=5", mockServiceKey, "")
	wantResponse(t, rec, http.StatusOK, `"items":[]`)
}

func TestSimulationBlockedPurchasesErrors(t *testing.T) {
	mux, service := simulationMux(t)
	service.EXPECT().BlockedPurchases(gomock.Any(), "unknown", int64(0), 20).Return(nil, int64(0), models.ErrSimulationNotFound)
	service.EXPECT().BlockedPurchases(gomock.Any(), "sim-2", int64(0), 20).Return(nil, int64(0), errors.New("connection refused"))

	wantResponse(t, send(mux, "GET", "/simulations/unknown/blocked", mockServiceKey, ""), http.StatusNotFound)
	wantResponse(t, send(mux, "GET", "/simulations/sim-2/blocked", mockServiceKey, ""), http.StatusServiceUnavailable)
	for _, query := range []string{"?before=x", "?before=0", "?before=-3", "?limit=0", "?limit=101"} {
		wantResponse(t, send(mux, "GET", "/simulations/sim-1/blocked"+query, mockServiceKey, ""), http.StatusBadRequest)
	}
	for _, key := range []string{"", "unknown", "bad"} {
		wantResponse(t, send(mux, "GET", "/simulations/sim-1/blocked", key, ""), http.StatusUnauthorized)
	}
}
