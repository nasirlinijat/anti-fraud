package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteServerError(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(NewLogger(&logs))
	t.Cleanup(func() { slog.SetDefault(previous) })

	tests := []struct {
		name       string
		err        error
		status     int
		retryAfter string
		body       string
		level      string
		msg        string
	}{
		{"unreachable database", fmt.Errorf("read queue: %w", unreachableDatabaseError(t)), http.StatusServiceUnavailable, "1", DatabaseUnavailableMessage, "WARN", "read queue: database unavailable"},
		{"any other error", errors.New("read queue: bad row"), http.StatusInternalServerError, "", "internal error", "ERROR", "read queue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()
			rec := httptest.NewRecorder()
			WriteServerError(context.Background(), rec, "read queue", tt.err, "purchase_id", 7)

			var body ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if rec.Code != tt.status || rec.Header().Get("Retry-After") != tt.retryAfter || body.Error != tt.body {
				t.Fatalf("status %d, Retry-After %q, body %q", rec.Code, rec.Header().Get("Retry-After"), body.Error)
			}
			var line map[string]any
			if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
				t.Fatalf("log %q: %v", logs.String(), err)
			}
			if line["level"] != tt.level || line["msg"] != tt.msg || line["purchase_id"] != float64(7) || line["error"] == nil {
				t.Fatalf("log line %v, want %s %q with purchase_id and error", line, tt.level, tt.msg)
			}
		})
	}
}

func TestLogServerErrorOnlyLogs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(NewLogger(&logs))
	t.Cleanup(func() { slog.SetDefault(previous) })

	tests := []struct {
		name  string
		err   error
		level string
		msg   string
	}{
		{"unreachable database", fmt.Errorf("claim review: %w", unreachableDatabaseError(t)), "WARN", "claim review: database unavailable"},
		{"any other error", errors.New("claim review: bad row"), "ERROR", "claim review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()

			LogServerError(context.Background(), "claim review", tt.err, "purchase_id", 7)

			var line map[string]any
			if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
				t.Fatalf("log %q: %v", logs.String(), err)
			}
			if line["level"] != tt.level || line["msg"] != tt.msg || line["purchase_id"] != float64(7) || line["error"] == nil {
				t.Fatalf("log line %v, want %s %q with purchase_id and error", line, tt.level, tt.msg)
			}
		})
	}
}
