package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// AccountEventService records an account event and reports whether it is new.
type AccountEventService interface {
	Record(ctx context.Context, e models.AccountEvent) (bool, error)
}

// maxAccountEventIDBytes is the longest event ID, player ID, or device ID an
// account event may have.
const maxAccountEventIDBytes = 256

// AccountEventHandler serves the account events endpoint.
type AccountEventHandler struct {
	service AccountEventService
	now     func() time.Time
	timeout time.Duration
}

func NewAccountEventHandler(service AccountEventService) *AccountEventHandler {
	return &AccountEventHandler{service: service, now: time.Now}
}

// UseTimeout limits how long recording one event may take. Zero means no
// limit.
func (h *AccountEventHandler) UseTimeout(timeout time.Duration) *AccountEventHandler {
	h.timeout = timeout
	return h
}

// RegisterAccountEventRoutes registers POST /account-events behind the
// service key, with the same time limit as POST /score.
func RegisterAccountEventRoutes(mux *http.ServeMux, h *AccountEventHandler, apiKey string) {
	var handler http.Handler = h
	if h.timeout > 0 {
		handler = WithTimeout(h.timeout, handler)
	}
	mux.Handle("POST /account-events", platform.RequireAPIKey(apiKey, handler))
}

// accountEventRequest is the account event request body. The event time is
// RFC 3339 and is converted to UTC.
type accountEventRequest struct {
	EventID   string    `json:"event_id"`
	PlayerID  string    `json:"player_id"`
	EventType string    `json:"event_type"`
	EventTime time.Time `json:"event_time"`
	DeviceID  *string   `json:"device_id"`
	IPCountry *string   `json:"ip_country"`
}

type accountEventResponse struct {
	EventID   string                  `json:"event_id"`
	PlayerID  string                  `json:"player_id"`
	EventType models.AccountEventType `json:"event_type"`
	EventTime time.Time               `json:"event_time"`
	DeviceID  *string                 `json:"device_id"`
	IPCountry *string                 `json:"ip_country"`
}

// ServeHTTP records one account event. It returns 201 for a new event and 200
// for a retry with the same fields.
func (h *AccountEventHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req accountEventRequest
	if status, resp := decode(w, r, &req); resp != nil {
		platform.WriteJSON(w, status, resp)
		return
	}
	e, fields := req.accountEvent(h.now())
	if len(fields) > 0 {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: fields})
		return
	}

	created, err := h.service.Record(r.Context(), e)
	switch {
	case errors.Is(err, models.ErrDuplicateAccountEvent):
		platform.WriteJSON(w, http.StatusConflict, platform.ErrorResponse{
			Error:  "an account event with this event ID was already recorded",
			Fields: map[string]string{"event_id": "is already recorded"},
		})
	case err != nil && (r.Context().Err() != nil || errors.Is(err, context.DeadlineExceeded)):
		slog.WarnContext(r.Context(), "record account event stopped", "event_id", e.EventID, "error", err)
		w.Header().Set("Retry-After", "1")
		platform.WriteError(w, http.StatusServiceUnavailable, "recording the account event took too long; retry later")
	case platform.DatabaseUnavailable(err):
		slog.WarnContext(r.Context(), "record account event: database unavailable", "event_id", e.EventID, "error", err)
		w.Header().Set("Retry-After", "1")
		platform.WriteError(w, http.StatusServiceUnavailable, "the database is unavailable, nothing was saved; retry later")
	case err != nil:
		slog.ErrorContext(r.Context(), "record account event", "event_id", e.EventID, "error", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
	case created:
		writeAccountEvent(w, http.StatusCreated, e)
	default:
		writeAccountEvent(w, http.StatusOK, e)
	}
}

func writeAccountEvent(w http.ResponseWriter, status int, e models.AccountEvent) {
	platform.WriteJSON(w, status, accountEventResponse{
		EventID:   e.EventID,
		PlayerID:  e.PlayerID,
		EventType: e.EventType,
		EventTime: e.EventTime,
		DeviceID:  e.DeviceID,
		IPCountry: e.IPCountry,
	})
}

// accountEvent validates the request and converts it to an AccountEvent. It
// returns the problems found, keyed by field name. Empty optional strings
// count as omitted. Event times in the past are allowed, so recorded traffic
// can be replayed. The IP country is converted to upper case.
func (req accountEventRequest) accountEvent(now time.Time) (models.AccountEvent, map[string]string) {
	fields := map[string]string{}
	e := models.AccountEvent{
		EventID:   strings.TrimSpace(req.EventID),
		PlayerID:  strings.TrimSpace(req.PlayerID),
		EventType: models.AccountEventType(strings.TrimSpace(req.EventType)),
		EventTime: req.EventTime.UTC(),
		DeviceID:  optional(req.DeviceID),
		IPCountry: optional(req.IPCountry),
	}
	checkAccountEventID(fields, "event_id", e.EventID, true)
	checkAccountEventID(fields, "player_id", e.PlayerID, true)
	if e.DeviceID != nil {
		checkAccountEventID(fields, "device_id", *e.DeviceID, false)
	}
	if !slices.Contains(models.AccountEventTypes, e.EventType) {
		fields["event_type"] = "must be login or password_reset"
	}
	if req.EventTime.IsZero() {
		fields["event_time"] = "is required"
	} else if e.EventTime.Sub(now) > maxClockSkew {
		fields["event_time"] = fmt.Sprintf("must not be more than %d minutes in the future", int(maxClockSkew.Minutes()))
	}
	if e.IPCountry != nil {
		country := strings.ToUpper(*e.IPCountry)
		e.IPCountry = &country
		if !isCountryCode(country) {
			fields["ip_country"] = "must be a two-letter country code"
		}
	}
	return e, fields
}

// checkAccountEventID records a problem in fields when the ID is missing,
// too long, or contains control characters.
func checkAccountEventID(fields map[string]string, name, value string, required bool) {
	switch {
	case value == "" && required:
		fields[name] = "is required"
	case len(value) > maxAccountEventIDBytes:
		fields[name] = fmt.Sprintf("must not be longer than %d bytes", maxAccountEventIDBytes)
	case strings.ContainsFunc(value, unicode.IsControl):
		fields[name] = "must not contain control characters"
	}
}

// isCountryCode reports whether s is two upper-case ASCII letters.
func isCountryCode(s string) bool {
	return len(s) == 2 && 'A' <= s[0] && s[0] <= 'Z' && 'A' <= s[1] && s[1] <= 'Z'
}
