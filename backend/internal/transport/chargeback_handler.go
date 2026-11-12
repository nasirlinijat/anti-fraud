package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/chargeback"
)

// ChargebackService records chargebacks reported by the processor.
type ChargebackService interface {
	Record(ctx context.Context, c models.Chargeback, now time.Time) (models.StoredChargeback, bool, error)
}

// ChargebackHandler serves the processor's chargeback webhook.
type ChargebackHandler struct {
	service ChargebackService
	secret  string
	now     func() time.Time
}

func NewChargebackHandler(service ChargebackService, secret string) *ChargebackHandler {
	return &ChargebackHandler{service: service, secret: secret, now: time.Now}
}

// RegisterChargebackRoutes registers POST /chargebacks. The webhook is
// authenticated by its signature, not by an API key.
func RegisterChargebackRoutes(mux *http.ServeMux, h *ChargebackHandler) {
	mux.Handle("POST /chargebacks", h)
}

type chargebackRequest struct {
	ChargebackID  string    `json:"chargeback_id"`
	TransactionID string    `json:"transaction_id"`
	ReasonCode    string    `json:"reason_code"`
	AmountMinor   int64     `json:"amount_minor"`
	ReceivedAt    time.Time `json:"received_at"`
}

type chargebackResponse struct {
	ChargebackID  string    `json:"chargeback_id"`
	TransactionID string    `json:"transaction_id"`
	ReasonCode    string    `json:"reason_code"`
	AmountMinor   int64     `json:"amount_minor"`
	ReceivedAt    time.Time `json:"received_at"`
	RecordedAt    time.Time `json:"recorded_at"`
}

// ServeHTTP verifies the signature of one chargeback delivery, records it and
// responds with 202 for a new chargeback or 200 for a repeated delivery.
func (h *ChargebackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.secret == "" {
		platform.WriteError(w, http.StatusServiceUnavailable, "the chargeback webhook is not configured")
		return
	}
	body, ok := readWebhookBody(w, r)
	if !ok {
		return
	}
	if err := platform.VerifyWebhook(h.secret, r.Header, body, h.now()); err != nil {
		platform.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req chargebackRequest
	if problem := decodeChargeback(body, &req); problem != nil {
		platform.WriteJSON(w, http.StatusBadRequest, problem)
		return
	}
	stored, created, err := h.service.Record(r.Context(), req.chargeback(), h.now())
	if err != nil {
		writeChargebackError(w, r, req.ChargebackID, err)
		return
	}
	writeChargebackResponse(w, stored, created)
}

// readWebhookBody reads the raw body, which the signature covers. It writes
// 413 and returns false for a body over the size limit.
func readWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		platform.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body must not exceed %d bytes", maxBodyBytes))
		return nil, false
	case err != nil:
		platform.WriteError(w, http.StatusBadRequest, "request body could not be read")
		return nil, false
	}
	return body, true
}

// decodeChargeback reads exactly one JSON object from the verified body.
func decodeChargeback(body []byte, req *chargebackRequest) *platform.ErrorResponse {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	err := dec.Decode(req)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		return &platform.ErrorResponse{Error: "request body must contain a single JSON object"}
	}
	if err == nil {
		return nil
	}
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr) && typeErr.Field != "":
		return &platform.ErrorResponse{Error: "invalid request", Fields: map[string]string{typeErr.Field: typeMessage(typeErr.Type)}}
	case errors.Is(err, io.EOF):
		return &platform.ErrorResponse{Error: "request body is empty"}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return &platform.ErrorResponse{Error: "invalid request", Fields: map[string]string{field: "is not a known field"}}
	default:
		return &platform.ErrorResponse{Error: "request body is not a valid chargeback JSON object; times must be RFC 3339"}
	}
}

func (req chargebackRequest) chargeback() models.Chargeback {
	return models.Chargeback{
		ChargebackID:  req.ChargebackID,
		TransactionID: req.TransactionID,
		Reason:        models.ChargebackReason(req.ReasonCode),
		AmountMinor:   req.AmountMinor,
		ReceivedAt:    req.ReceivedAt,
	}
}

func writeChargebackError(w http.ResponseWriter, r *http.Request, chargebackID string, err error) {
	var invalid *chargeback.ValidationError
	switch {
	case errors.As(err, &invalid):
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: invalid.Fields})
	case errors.Is(err, models.ErrPurchaseNotFound):
		platform.WriteJSON(w, http.StatusNotFound, platform.ErrorResponse{
			Error: "no purchase was scored with this transaction ID", Fields: map[string]string{"transaction_id": "is unknown"},
		})
	case errors.Is(err, models.ErrChargebackConflict):
		platform.WriteJSON(w, http.StatusConflict, platform.ErrorResponse{
			Error: err.Error(), Fields: map[string]string{"chargeback_id": "is already recorded"},
		})
	case platform.DatabaseUnavailable(err):
		slog.WarnContext(r.Context(), "record chargeback: database unavailable", "chargeback_id", chargebackID, "error", err)
		w.Header().Set("Retry-After", "1")
		platform.WriteError(w, http.StatusServiceUnavailable, "the database is unavailable, nothing was saved; retry later")
	default:
		slog.ErrorContext(r.Context(), "record chargeback", "chargeback_id", chargebackID, "error", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeChargebackResponse(w http.ResponseWriter, c models.StoredChargeback, created bool) {
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	platform.WriteJSON(w, status, chargebackResponse{
		ChargebackID:  c.ChargebackID,
		TransactionID: c.TransactionID,
		ReasonCode:    string(c.Reason),
		AmountMinor:   c.AmountMinor,
		ReceivedAt:    c.ReceivedAt,
		RecordedAt:    c.RecordedAt,
	})
}
