package simulator_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

type scorerFunc func(context.Context, simulator.Request) (simulator.Result, error)

func (f scorerFunc) Score(ctx context.Context, req simulator.Request) (simulator.Result, error) {
	return f(ctx, req)
}

func TestLiveUsesSeededSequenceWithWallClockTimes(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.Start = time.Now().UTC()
	cfg.SuspiciousShare = 0.3
	want := generate(t, cfg, 2_000)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	accounts := make(map[string]time.Time)
	ids := make(map[string]bool)
	calls := 0
	scorer := scorerFunc(func(_ context.Context, req simulator.Request) (simulator.Result, error) {
		expect := want[calls].Request
		if req.EventTime.Location() != time.UTC || req.EventTime.Before(cfg.Start) || req.EventTime.After(time.Now()) {
			t.Fatalf("purchase %d has event time outside the run: %v", calls, req.EventTime)
		}
		if ids[req.TransactionID] {
			t.Fatalf("duplicate transaction ID %q", req.TransactionID)
		}
		ids[req.TransactionID] = true
		if req.AccountCreatedAt == nil || req.AccountCreatedAt.After(req.EventTime) {
			t.Fatalf("purchase %d has invalid account creation time: %v", calls, req.AccountCreatedAt)
		}
		if created, seen := accounts[req.PlayerID]; seen && !created.Equal(*req.AccountCreatedAt) {
			t.Fatalf("account creation time changed for %s", req.PlayerID)
		}
		accounts[req.PlayerID] = *req.AccountCreatedAt
		if want[calls].Pattern == simulator.PatternNormal || want[calls].Pattern == simulator.PatternAccountTakeover {
			if !req.AccountCreatedAt.Equal(*expect.AccountCreatedAt) {
				t.Fatalf("regular account was not anchored to the run start: %v, want %v", req.AccountCreatedAt, expect.AccountCreatedAt)
			}
		}

		req.EventTime, req.AccountCreatedAt = expect.EventTime, expect.AccountCreatedAt
		if !reflect.DeepEqual(req, expect) {
			t.Fatalf("purchase %d differs from its backfill counterpart", calls)
		}
		calls++
		if calls == len(want) {
			cancel()
		}
		return simulator.Result{Outcome: "allow"}, nil
	})

	sum, err := simulator.RunLive(ctx, scorer, cfg, 0, nil)
	if !errors.Is(err, context.Canceled) || sum.Sent != len(want) || sum.Failed != 0 {
		t.Fatalf("sent %d, failed %d, error %v", sum.Sent, sum.Failed, err)
	}
	patternCounts := make(map[simulator.Pattern]int)
	for _, p := range want {
		patternCounts[p.Pattern]++
	}
	for pattern, count := range patternCounts {
		if got := sum.Outcomes[pattern]["allow"]; got != count {
			t.Errorf("%s: counted %d outcomes, want %d", pattern, got, count)
		}
	}
}

func TestLivePacesAtSendTimeAndStopsDuringWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now().UTC()
		cfg := simulator.DefaultConfig()
		cfg.Start = start
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			time.Sleep(1250 * time.Millisecond)
			cancel()
		}()

		var elapsed []time.Duration
		scorer := scorerFunc(func(_ context.Context, req simulator.Request) (simulator.Result, error) {
			if !req.EventTime.Equal(time.Now()) {
				t.Errorf("event time %v, want send time %v", req.EventTime, time.Now())
			}
			elapsed = append(elapsed, req.EventTime.Sub(start))
			return simulator.Result{Outcome: "allow"}, nil
		})
		sum, err := simulator.RunLive(ctx, scorer, cfg, 2, nil)
		want := []time.Duration{0, 500 * time.Millisecond, time.Second}
		if !errors.Is(err, context.Canceled) || sum.Sent != len(want) || sum.Failed != 0 || !reflect.DeepEqual(elapsed, want) {
			t.Fatalf("sent %d at %v, failed %d, error %v", sum.Sent, elapsed, sum.Failed, err)
		}
		if got := time.Since(start); got != 1250*time.Millisecond {
			t.Fatalf("stopped at %v, want immediate cancellation during rate wait", got)
		}
	})
}

func TestLiveDoesNotCountAnInterruptedRequestAsAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	scorer := scorerFunc(func(ctx context.Context, _ simulator.Request) (simulator.Result, error) {
		calls++
		if calls == 1 {
			return simulator.Result{Outcome: "allow"}, nil
		}
		cancel()
		return simulator.Result{}, fmt.Errorf("read response: %w", ctx.Err())
	})
	sum, err := simulator.RunLive(ctx, scorer, simulator.DefaultConfig(), 0, func(err error) {
		t.Errorf("interrupted request reported as failed: %v", err)
	})
	if !errors.Is(err, context.Canceled) || sum.Sent != 1 || sum.Failed != 0 || calls != 2 {
		t.Fatalf("sent %d, failed %d, calls %d, error %v", sum.Sent, sum.Failed, calls, err)
	}
}

func TestLiveStopsAfterConsecutiveFailures(t *testing.T) {
	const limit = simulator.MaxFailuresInRow
	failure := errors.New("score unavailable")
	calls := 0
	reported := 0
	scorer := scorerFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
		calls++
		if calls == limit {
			return simulator.Result{Outcome: "review"}, nil
		}
		return simulator.Result{}, failure
	})
	sum, err := simulator.RunLive(t.Context(), scorer, simulator.DefaultConfig(), 0, func(err error) {
		if !errors.Is(err, failure) {
			t.Errorf("unexpected failure: %v", err)
		}
		reported++
	})
	if !errors.Is(err, failure) || sum.Sent != 1 || sum.Failed != 2*limit-1 || reported != sum.Failed {
		t.Fatalf("sent %d, failed %d, reported %d, error %v", sum.Sent, sum.Failed, reported, err)
	}
}

func TestLiveRejectsInvalidRates(t *testing.T) {
	scorer := scorerFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
		t.Fatal("sent a purchase at an invalid rate")
		return simulator.Result{}, nil
	})
	for _, rate := range []float64{-1, math.NaN(), math.Inf(1), 1e12, 1e-20} {
		if _, err := simulator.RunLive(t.Context(), scorer, simulator.DefaultConfig(), rate, nil); err == nil {
			t.Errorf("accepted invalid rate %v", rate)
		}
	}
}
