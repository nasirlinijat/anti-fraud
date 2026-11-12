package transport

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

func TestRateLimiterTokenBucket(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	l := newRateLimiter(1, 2)
	l.now = func() time.Time { return now }

	steps := []struct {
		name     string
		advance  time.Duration
		key      string
		allowed  bool
		wantWait time.Duration
	}{
		{"first request uses the burst", 0, "key-a", true, 0},
		{"second request uses the burst", 0, "key-a", true, 0},
		{"third request waits a whole second", 0, "key-a", false, time.Second},
		{"half a second later it waits half a second", 500 * time.Millisecond, "key-a", false, 500 * time.Millisecond},
		{"a second after the burst one token is back", 500 * time.Millisecond, "key-a", true, 0},
		{"another key has its own bucket", 0, "key-b", true, 0},
		{"a long pause refills only up to the burst", time.Hour, "key-a", true, 0},
		{"second token after the pause", 0, "key-a", true, 0},
		{"the burst is not exceeded", 0, "key-a", false, time.Second},
	}
	for _, s := range steps {
		now = now.Add(s.advance)
		allowed, wait := l.allow(s.key)
		if allowed != s.allowed || wait != s.wantWait {
			t.Fatalf("%s: allow = %v, wait %v; want %v, %v", s.name, allowed, wait, s.allowed, s.wantWait)
		}
	}
}

func limitedScoreMux(t *testing.T, limits platform.ScoreLimits) (*http.ServeMux, *mocks.MockScoreService) {
	service := mocks.NewMockScoreService(gomock.NewController(t))
	mux := http.NewServeMux()
	RegisterScoreRoutes(mux, NewScoreHandler(service).UseLimits(limits), mockServiceKey)
	return mux, service
}

func TestScoreReturns429OverTheRateLimit(t *testing.T) {
	mux, service := limitedScoreMux(t, platform.ScoreLimits{RatePerSecond: 0.5, Burst: 2})
	service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.Decision{Result: scoring.Result{Outcome: scoring.OutcomeAllow}}, nil).Times(2)

	for range 5 {
		wantResponse(t, send(mux, http.MethodPost, "/score", "wrong-key", minimalBody), http.StatusUnauthorized)
	}
	wantResponse(t, send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody), http.StatusOK)
	wantResponse(t, send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody), http.StatusOK)

	limited := send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody)
	wantResponse(t, limited, http.StatusTooManyRequests, "too many score requests")
	if got := limited.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2 seconds at half a request per second", got)
	}
}

func TestScoreDeadlineReturns503(t *testing.T) {
	mux, service := limitedScoreMux(t, platform.ScoreLimits{Timeout: 50 * time.Millisecond})
	service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ models.Purchase, _ time.Time) (models.Decision, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 50*time.Millisecond {
				t.Errorf("deadline %v, %v; want one within 50ms", deadline, ok)
			}
			<-ctx.Done()
			return models.Decision{}, ctx.Err()
		})

	started := time.Now()
	rec := send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the request took %v, want it stopped near the 50ms deadline", elapsed)
	}
	wantResponse(t, rec, http.StatusServiceUnavailable, "nothing was saved")
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", rec.Header().Get("Retry-After"))
	}
}

func TestScoreWithoutLimitsHasNoDeadline(t *testing.T) {
	mux, service := limitedScoreMux(t, platform.ScoreLimits{})
	service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ models.Purchase, _ time.Time) (models.Decision, error) {
			if _, ok := ctx.Deadline(); ok {
				t.Error("a deadline was set without a timeout")
			}
			return models.Decision{Result: scoring.Result{Outcome: scoring.OutcomeAllow}}, nil
		}).Times(3)
	for range 3 {
		wantResponse(t, send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody), http.StatusOK)
	}
}
