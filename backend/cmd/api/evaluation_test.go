package main

import (
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"testing"
)

func TestEvaluationAccessDoesNotEnableSimulation(t *testing.T) {
	for _, database := range []string{"", ":invalid:", "postgres://unused:unused@127.0.0.1:1/test?sslmode=disable"} {
		cfg := platform.Config{LabelsDatabaseURL: database}
		pool, err := openEvaluationPool(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if pool != nil {
			pool.Close()
		}
		if manager := newSimulationManager(cfg, nil, nil); manager != nil {
			t.Fatal("disabled controller was started")
		}
	}
	_, err := openEvaluationPool(platform.Config{SimulationEnabled: true, LabelsDatabaseURL: ":invalid:"})
	if err == nil {
		t.Fatal("enabled simulations accepted invalid evaluation configuration")
	}
	if _, err := openEvaluationPool(platform.Config{SimulationEnabled: true}); err == nil {
		t.Fatal("enabled simulations accepted absent evaluation access")
	}
}
