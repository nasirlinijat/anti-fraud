package transport

import (
	"context"
	"crypto/sha256"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

type rateLimiter struct {
	rate    float64
	burst   float64
	now     func() time.Time
	mu      sync.Mutex
	buckets map[[sha256.Size]byte]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(ratePerSecond float64, burst int) *rateLimiter {
	return &rateLimiter{
		rate:    ratePerSecond,
		burst:   float64(burst),
		now:     time.Now,
		buckets: map[[sha256.Size]byte]*bucket{},
	}
}

func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	id := sha256.Sum256([]byte(key))
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, found := l.buckets[id]
	if !found {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[id] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

func RateLimit(l *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, wait := l.allow(r.Header.Get(platform.APIKeyHeader))
		if !allowed {
			seconds := int(math.Ceil(wait.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(max(seconds, 1)))
			platform.WriteError(w, http.StatusTooManyRequests, "too many score requests, retry later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func WithTimeout(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
