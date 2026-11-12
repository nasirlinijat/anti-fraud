package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/chargeback"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

const testChargebackSecret = "chargeback-secret-of-at-least-32-bytes"

var chargebackNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// unusedChargebackService fails the test when the handler reaches the service.
type unusedChargebackService struct{ t *testing.T }

func (s unusedChargebackService) Record(context.Context, models.Chargeback, time.Time) (models.StoredChargeback, bool, error) {
	s.t.Fatal("the service must not be called")
	return models.StoredChargeback{}, false, nil
}

func chargebackServer(service ChargebackService, secret string) http.Handler {
	h := NewChargebackHandler(service, secret)
	h.now = func() time.Time { return chargebackNow }
	mux := http.NewServeMux()
	RegisterChargebackRoutes(mux, h)
	return mux
}

func signedChargeback(body, secret string, signedAt time.Time) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/chargebacks", strings.NewReader(body))
	req.Header.Set(platform.SignatureTimestampHeader, strconv.FormatInt(signedAt.Unix(), 10))
	req.Header.Set(platform.SignatureHeader, platform.SignWebhook(secret, signedAt, []byte(body)))
	return req
}

func serveChargeback(t *testing.T, handler http.Handler, req *http.Request) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

const chargebackBody = `{"chargeback_id":"cb-1","transaction_id":"cb-txn-1","reason_code":"fraud_card_not_present","amount_minor":1999,"received_at":"2026-11-10T12:00:00Z"}`

func TestChargebackWebhookIsUnavailableWithoutASecret(t *testing.T) {
	handler := chargebackServer(unusedChargebackService{t}, "")

	status, body := serveChargeback(t, handler, signedChargeback(chargebackBody, "", chargebackNow))

	if status != http.StatusServiceUnavailable || body["error"] != "the chargeback webhook is not configured" {
		t.Fatalf("got %d %v, want 503 not configured", status, body)
	}
}

func TestChargebackWebhookRejectsBadSignatures(t *testing.T) {
	handler := chargebackServer(unusedChargebackService{t}, testChargebackSecret)
	tests := map[string]*http.Request{
		"wrong secret":    signedChargeback(chargebackBody, "another-secret-of-at-least-32-bytes!", chargebackNow),
		"stale timestamp": signedChargeback(chargebackBody, testChargebackSecret, chargebackNow.Add(-6*time.Minute)),
		"missing headers": httptest.NewRequest(http.MethodPost, "/chargebacks", strings.NewReader(chargebackBody)),
		"body changed after signing": func() *http.Request {
			req := signedChargeback(chargebackBody, testChargebackSecret, chargebackNow)
			req.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Replace(chargebackBody, "1999", "2999", 1))).Body
			return req
		}(),
	}
	for name, req := range tests {
		t.Run(name, func(t *testing.T) {
			status, body := serveChargeback(t, handler, req)

			if status != http.StatusUnauthorized || body["error"] != platform.ErrInvalidSignature.Error() {
				t.Fatalf("got %d %v, want 401", status, body)
			}
		})
	}
}

func TestChargebackWebhookRejectsInvalidBodies(t *testing.T) {
	handler := chargebackServer(unusedChargebackService{t}, testChargebackSecret)
	tests := []struct {
		name, body, want string
	}{
		{"not JSON", `{`, "not a valid chargeback"},
		{"empty", ``, "empty"},
		{"unknown field", `{"chargeback_id":"cb-1","currency":"EUR"}`, "invalid request"},
		{"wrong type", `{"amount_minor":"10"}`, "invalid request"},
		{"bad time", `{"received_at":"yesterday"}`, "RFC 3339"},
		{"two objects", `{} {}`, "single JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := serveChargeback(t, handler, signedChargeback(tt.body, testChargebackSecret, chargebackNow))

			if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), tt.want) {
				t.Fatalf("got %d %v, want 400 containing %q", status, body, tt.want)
			}
		})
	}
}

func TestChargebackWebhookRejectsALargeBody(t *testing.T) {
	handler := chargebackServer(unusedChargebackService{t}, testChargebackSecret)
	large := `{"chargeback_id":"` + strings.Repeat("x", maxBodyBytes) + `"}`

	status, _ := serveChargeback(t, handler, signedChargeback(large, testChargebackSecret, chargebackNow))

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", status)
	}
}

// chargebackDatabaseServer serves the webhook with the real service and store
// in a rolled-back transaction that holds the purchase cb-txn-1.
func chargebackDatabaseServer(t *testing.T) http.Handler {
	t.Helper()
	tx := testTx(t)
	p := minimalPurchase("cb-txn-1")
	d := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}, ModelInputs: map[string]float64{}}
	if _, err := storage.NewPurchaseStore(tx).Save(context.Background(), p, d); err != nil {
		t.Fatalf("save purchase: %v", err)
	}
	return chargebackServer(chargeback.NewService(storage.NewChargebackStore(tx)), testChargebackSecret)
}

func TestChargebackWebhookRecordsAChargebackOnce(t *testing.T) {
	handler := chargebackDatabaseServer(t)

	status, first := serveChargeback(t, handler, signedChargeback(chargebackBody, testChargebackSecret, chargebackNow))
	if status != http.StatusAccepted {
		t.Fatalf("first delivery = %d %v, want 202", status, first)
	}
	status, again := serveChargeback(t, handler, signedChargeback(chargebackBody, testChargebackSecret, chargebackNow.Add(time.Minute)))

	if status != http.StatusOK {
		t.Fatalf("repeated delivery = %d %v, want 200", status, again)
	}
	if first["recorded_at"] != again["recorded_at"] || again["reason_code"] != "fraud_card_not_present" || again["transaction_id"] != "cb-txn-1" {
		t.Errorf("repeat returned %v, want the first result %v", again, first)
	}
}

func TestChargebackWebhookRejectsAChangedRepeat(t *testing.T) {
	handler := chargebackDatabaseServer(t)
	serveChargeback(t, handler, signedChargeback(chargebackBody, testChargebackSecret, chargebackNow))
	changed := strings.Replace(chargebackBody, "fraud_card_not_present", "duplicate", 1)

	status, body := serveChargeback(t, handler, signedChargeback(changed, testChargebackSecret, chargebackNow))

	if status != http.StatusConflict {
		t.Fatalf("got %d %v, want 409", status, body)
	}
}

func TestChargebackWebhookReturnsNotFoundForAnUnknownTransaction(t *testing.T) {
	handler := chargebackDatabaseServer(t)
	unknown := strings.Replace(chargebackBody, "cb-txn-1", "cb-txn-unknown", 1)

	status, body := serveChargeback(t, handler, signedChargeback(unknown, testChargebackSecret, chargebackNow))

	if status != http.StatusNotFound {
		t.Fatalf("got %d %v, want 404", status, body)
	}
}

func TestChargebackWebhookNamesInvalidFields(t *testing.T) {
	handler := chargebackDatabaseServer(t)
	tooEarly := strings.Replace(chargebackBody, "2026-11-10T12:00:00Z", "2026-11-01T00:00:00Z", 1)

	status, body := serveChargeback(t, handler, signedChargeback(tooEarly, testChargebackSecret, chargebackNow))

	fields, _ := body["fields"].(map[string]any)
	if status != http.StatusBadRequest || fields["received_at"] != "must not be before the purchase event time" {
		t.Fatalf("got %d %v, want 400 naming received_at", status, body)
	}
}

func TestChargebackWebhookRejectsControlCharactersInIDs(t *testing.T) {
	handler := chargebackDatabaseServer(t)
	tests := map[string]struct{ body, field string }{
		"NUL in chargeback ID":  {strings.Replace(chargebackBody, `"cb-1"`, `"cb-\u00001"`, 1), "chargeback_id"},
		"tab in transaction ID": {strings.Replace(chargebackBody, `"cb-txn-1"`, `"cb-\ttxn-1"`, 1), "transaction_id"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			status, body := serveChargeback(t, handler, signedChargeback(tt.body, testChargebackSecret, chargebackNow))

			fields, _ := body["fields"].(map[string]any)
			if status != http.StatusBadRequest || fields[tt.field] != "must not contain control characters" {
				t.Fatalf("got %d %v, want 400 naming %s", status, body, tt.field)
			}
		})
	}
}
