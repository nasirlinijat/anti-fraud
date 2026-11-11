package simulator

import (
	"context"
	"reflect"
	"testing"
)

func TestSelectedScenarios(t *testing.T) {
	for _, p := range Patterns {
		cfg := DefaultConfig()
		cfg.Scenarios = []Pattern{p}
		a, e := Generate(cfg, 40)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := Generate(cfg, 40)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("not deterministic")
		}
		for _, v := range a {
			if v.Pattern != p {
				t.Fatalf("got %s want %s", v.Pattern, p)
			}
		}
	}
}
func TestCancelledGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := GenerateContext(ctx, DefaultConfig(), 100); e == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestEmptySelection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Scenarios = []Pattern{}
	if _, e := Generate(cfg, 1); e == nil {
		t.Fatal("accepted empty selection")
	}
}

func TestFullSelectionPreservesDefaultTraffic(t *testing.T) {
	cfg := DefaultConfig()
	want, e := Generate(cfg, 500)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Scenarios = append([]Pattern(nil), Patterns...)
	got, e := Generate(cfg, 500)
	if e != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("default traffic changed: %v", e)
	}
}
