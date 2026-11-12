package transport

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	requestIDHeader    = "X-Request-ID"
	maxRequestIDLength = 64
)

func Middleware(next http.Handler) http.Handler {
	handler := RequestID(Logging(Recover(next)))
	if platform.TracingEnabled() {
		handler = otelhttp.NewHandler(handler, "http.server",
			otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/health" && r.URL.Path != "/ready" }),
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method }),
		)
	}
	return SecurityHeaders(handler)
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !validRequestID(id) {
			id = rand.Text()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(platform.WithRequestID(r.Context(), id)))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for _, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rec *statusRecorder) WriteHeader(code int) {
	if !rec.wroteHeader {
		rec.status, rec.wroteHeader = code, true
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if !rec.wroteHeader {
		rec.status, rec.wroteHeader = http.StatusOK, true
	}
	return rec.ResponseWriter.Write(b)
}

func (rec *statusRecorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := routeOf(r)
		if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
			span.SetName(r.Method + " " + route)
			span.SetAttributes(attribute.String("http.route", route), attribute.String("request_id", platform.RequestID(r.Context())))
		}
		if (route == "/health" || route == "/ready") && rec.status < http.StatusBadRequest {
			return
		}
		level := slog.LevelInfo
		if rec.status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		slog.LogAttrs(r.Context(), level, "http request",
			slog.Group("http",
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status_code", rec.status),
			),
			slog.Int64("duration", time.Since(started).Nanoseconds()),
		)
	})
}

func routeOf(r *http.Request) string {
	if r.Pattern == "" {
		return "unmatched"
	}
	if _, path, found := strings.Cut(r.Pattern, " "); found {
		return path
	}
	return r.Pattern
}

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			slog.ErrorContext(r.Context(), "panic recovered",
				"error", fmt.Sprint(v),
				"stack", string(debug.Stack()),
			)
			if !rec.wroteHeader {
				platform.WriteError(rec, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(rec, r)
	})
}
