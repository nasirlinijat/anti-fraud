package transport

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(platform.NewLogger(&buf))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %s", raw)
		}
		lines = append(lines, line)
	}
	return lines
}

func findLine(t *testing.T, lines []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, line := range lines {
		if line["msg"] == msg {
			return line
		}
	}
	t.Fatalf("no %q line in %v", msg, lines)
	return nil
}

const secretPurchase = `{
	"transaction_id": "txn-log",
	"event_time": "2026-11-08T10:00:00Z",
	"amount_minor": 1999,
	"player_id": "player-log",
	"card_token": "card-token-secret-4111",
	"ip_address": "203.0.113.77"
}`

func scoreMux(t *testing.T) (http.Handler, *mocks.MockScoreService) {
	service := mocks.NewMockScoreService(gomock.NewController(t))
	mux := http.NewServeMux()
	RegisterScoreRoutes(mux, NewScoreHandler(service), mockServiceKey)
	return Middleware(mux), service
}

func TestRequestLogHasTheRequestAndNoSecrets(t *testing.T) {
	buf := captureLogs(t)
	handler, service := scoreMux(t)
	service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.Decision{Result: scoring.Result{Outcome: scoring.OutcomeAllow}}, nil)

	ok := send(handler, http.MethodPost, "/score", mockServiceKey, secretPurchase)
	rejected := send(handler, http.MethodPost, "/score", "wrong-key-secret-777", secretPurchase)
	if ok.Code != http.StatusOK || rejected.Code != http.StatusUnauthorized {
		t.Fatalf("statuses %d and %d, want 200 and 401", ok.Code, rejected.Code)
	}

	for _, secret := range []string{mockServiceKey, "wrong-key-secret-777", "card-token-secret-4111", "203.0.113.77"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("the log contains %q:\n%s", secret, buf.String())
		}
	}

	lines := logLines(t, buf)
	if len(lines) != 2 {
		t.Fatalf("%d log lines, want one per request:\n%s", len(lines), buf.String())
	}
	line := lines[0]
	httpAttrs, _ := line["http"].(map[string]any)
	if line["msg"] != "http request" || line["service"] != platform.ServiceName || line["level"] != "INFO" {
		t.Errorf("line %v, want an INFO http request of %s", line, platform.ServiceName)
	}
	if httpAttrs["method"] != "POST" || httpAttrs["route"] != "/score" || httpAttrs["status_code"] != float64(200) {
		t.Errorf("http attributes %v, want POST /score 200", httpAttrs)
	}
	if d, isNumber := line["duration"].(float64); !isNumber || d < 0 {
		t.Errorf("duration %v, want a number of nanoseconds", line["duration"])
	}
	if line["request_id"] == "" || line["request_id"] != ok.Header().Get("X-Request-ID") {
		t.Errorf("request_id %v, want the response header %q", line["request_id"], ok.Header().Get("X-Request-ID"))
	}
	if lines[1]["request_id"] == line["request_id"] {
		t.Error("two requests share one request ID")
	}
}

func TestHandlerErrorLogCarriesTheRequestID(t *testing.T) {
	buf := captureLogs(t)
	handler, service := scoreMux(t)
	service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.Decision{}, errServiceDown)

	rec := send(handler, http.MethodPost, "/score", mockServiceKey, secretPurchase)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	lines := logLines(t, buf)
	failure := findLine(t, lines, "score purchase")
	request := findLine(t, lines, "http request")
	id := rec.Header().Get("X-Request-ID")
	if failure["request_id"] != id || request["request_id"] != id {
		t.Fatalf("request IDs %v and %v, want both %q", failure["request_id"], request["request_id"], id)
	}
	if failure["level"] != "ERROR" || request["level"] != "ERROR" || failure["error"] != errServiceDown.Error() {
		t.Fatalf("lines %v and %v, want ERROR with the error", failure, request)
	}
}

func TestRequestIDFromTheClient(t *testing.T) {
	captureLogs(t)
	handler := Middleware(http.NewServeMux())
	tests := []struct {
		name string
		sent string
		keep bool
	}{
		{"valid", "trace-abc_123.4", true},
		{"missing", "", false},
		{"too long", strings.Repeat("a", 65), false},
		{"unsafe characters", "id with spaces", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/nothing", nil)
			if tt.sent != "" {
				req.Header.Set("X-Request-ID", tt.sent)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			got := rec.Header().Get("X-Request-ID")
			if tt.keep && got != tt.sent {
				t.Fatalf("X-Request-ID = %q, want the client's %q", got, tt.sent)
			}
			if !tt.keep && (got == "" || got == tt.sent) {
				t.Fatalf("X-Request-ID = %q, want a new ID", got)
			}
		})
	}
}

func TestRecoverReturns500AndLogsTheStack(t *testing.T) {
	buf := captureLogs(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })

	rec := send(Middleware(mux), http.MethodGet, "/boom", "", "")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("status %d body %s, want a 500 JSON error", rec.Code, rec.Body.String())
	}
	lines := logLines(t, buf)
	panicLine := findLine(t, lines, "panic recovered")
	if panicLine["error"] != "boom" || !strings.Contains(panicLine["stack"].(string), "middleware_test.go") {
		t.Fatalf("panic line %v, want the error and the stack", panicLine)
	}
	request := findLine(t, lines, "http request")
	if httpAttrs, _ := request["http"].(map[string]any); httpAttrs["status_code"] != float64(500) || httpAttrs["route"] != "/boom" {
		t.Fatalf("request line %v, want /boom with 500", request)
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	buf := captureLogs(t)
	mux := http.NewServeMux()
	platform.RegisterHealthRoutes(mux, nil)
	handler := Middleware(mux)
	for _, path := range []string{"/health", "/unknown"} {
		rec := send(handler, http.MethodGet, path, "", "")
		for header, want := range map[string]string{
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
			"Referrer-Policy":         "no-referrer",
		} {
			if got := rec.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
	}
	lines := logLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("lines %v, want only the unknown route logged, not the health check", lines)
	}
	if httpAttrs, _ := lines[0]["http"].(map[string]any); httpAttrs["route"] != "unmatched" || httpAttrs["status_code"] != float64(404) {
		t.Fatalf("line %v, want an unmatched 404", lines[0])
	}
}
