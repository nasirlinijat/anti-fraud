package platform

import "testing"

func TestSimulationFeatureConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("API_KEY", "test")
	for _, v := range []string{"", "false", "true", "invalid"} {
		t.Setenv("SIMULATION_ENABLED", v)
		c, e := LoadConfig()
		if v == "invalid" {
			if e == nil {
				t.Fatal("accepted invalid boolean")
			}
			continue
		}
		if e != nil || c.SimulationEnabled != (v == "true") {
			t.Fatalf("%q: %+v %v", v, c, e)
		}
	}
}
