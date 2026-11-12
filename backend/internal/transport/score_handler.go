package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

//go:generate mockgen -destination=mocks/mock_services.go -package=mocks . ScoreService,AccountEventService,ReviewService,RulesetService,BlocklistService,StatsService,SimulationService

const maxBodyBytes = 64 << 10

// maxClockSkew is how far in the future an event time may be. It allows for
// clocks that are slightly ahead; purchases further ahead would break the
// feature running totals, which assume purchases arrive in time order.
const maxClockSkew = 5 * time.Minute

// ScoreService scores a purchase and returns its saved decision.
type ScoreService interface {
	Score(ctx context.Context, p models.Purchase, started time.Time) (models.Decision, error)
}

// ScoreHandler serves the score endpoint.
type ScoreHandler struct {
	service ScoreService
	now     func() time.Time
	limits  platform.ScoreLimits
}

func NewScoreHandler(service ScoreService) *ScoreHandler {
	return &ScoreHandler{service: service, now: time.Now}
}

func (h *ScoreHandler) UseLimits(limits platform.ScoreLimits) *ScoreHandler {
	h.limits = limits
	return h
}

// RegisterScoreRoutes registers POST /score behind API key authentication.
func RegisterScoreRoutes(mux *http.ServeMux, h *ScoreHandler, apiKey string) {
	var handler http.Handler = h
	if h.limits.Timeout > 0 {
		handler = WithTimeout(h.limits.Timeout, handler)
	}
	if h.limits.RatePerSecond > 0 {
		handler = RateLimit(newRateLimiter(h.limits.RatePerSecond, h.limits.Burst), handler)
	}
	mux.Handle("POST /score", platform.RequireAPIKey(apiKey, handler))
}

// scoreRequest is the score request body. Amounts are integer minor units and
// times are RFC 3339; they are converted to UTC.
type scoreRequest struct {
	TransactionID        string     `json:"transaction_id"`
	EventTime            time.Time  `json:"event_time"`
	AmountMinor          int64      `json:"amount_minor"`
	PlayerID             string     `json:"player_id"`
	CardToken            string     `json:"card_token"`
	CardIssuer           *string    `json:"card_issuer"`
	CardNetwork          *string    `json:"card_network"`
	CardType             *string    `json:"card_type"`
	CardCountry          *string    `json:"card_country"`
	CardCategory         *string    `json:"card_category"`
	ProductCode          *string    `json:"product_code"`
	BillingRegion        *string    `json:"billing_region"`
	BillingCountry       *string    `json:"billing_country"`
	BillingDistance      *float64   `json:"billing_distance"`
	EmailDomain          *string    `json:"email_domain"`
	RecipientEmailDomain *string    `json:"recipient_email_domain"`
	DeviceType           *string    `json:"device_type"`
	DeviceName           *string    `json:"device_name"`
	DeviceID             *string    `json:"device_id"`
	IPAddress            *string    `json:"ip_address"`
	IPCountry            *string    `json:"ip_country"`
	AccountCreatedAt     *time.Time `json:"account_created_at"`
	ClientOS             *string    `json:"client_os"`
	ClientBrowser        *string    `json:"client_browser"`
	ScreenResolution     *string    `json:"screen_resolution"`
	CVVResult            *string    `json:"cvv_result"`
	AVSResult            *string    `json:"avs_result"`
	ThreeDSResult        *string    `json:"three_ds_result"`
}

type scoreResponse struct {
	TransactionID  string              `json:"transaction_id"`
	Outcome        scoring.Outcome     `json:"outcome"`
	Score          int                 `json:"score"`
	FiredRules     []scoring.FiredRule `json:"fired_rules"`
	RulesetVersion int                 `json:"ruleset_version"`
	ModelScore     *float64            `json:"model_score"`
	ModelVersion   *string             `json:"model_version"`
}

// ServeHTTP scores one purchase and returns its saved decision. A retry of a
// saved transaction returns the decision saved the first time.
func (h *ScoreHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var req scoreRequest
	if status, resp := decode(w, r, &req); resp != nil {
		platform.WriteJSON(w, status, resp)
		return
	}
	p, fields := req.purchase(h.now())
	if len(fields) > 0 {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: fields})
		return
	}

	decision, err := h.service.Score(r.Context(), p, started)
	switch {
	case errors.Is(err, models.ErrDuplicateTransaction):
		platform.WriteJSON(w, http.StatusConflict, platform.ErrorResponse{
			Error:  "a purchase with this transaction ID was already scored",
			Fields: map[string]string{"transaction_id": "is already scored"},
		})
	case err != nil && (r.Context().Err() != nil || errors.Is(err, context.DeadlineExceeded)):
		slog.WarnContext(r.Context(), "score purchase stopped", "transaction_id", p.TransactionID, "error", err)
		w.Header().Set("Retry-After", "1")
		platform.WriteError(w, http.StatusServiceUnavailable, "scoring took too long, nothing was saved; retry later")
	case platform.DatabaseUnavailable(err):
		slog.WarnContext(r.Context(), "score purchase: database unavailable", "transaction_id", p.TransactionID, "error", err)
		w.Header().Set("Retry-After", "1")
		platform.WriteError(w, http.StatusServiceUnavailable, "the database is unavailable, nothing was saved; retry later")
	case err != nil:
		slog.ErrorContext(r.Context(), "score purchase", "transaction_id", p.TransactionID, "error", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
	default:
		writeScoreResponse(w, p.TransactionID, decision)
	}
}

func writeScoreResponse(w http.ResponseWriter, transactionID string, d models.Decision) {
	firedRules := d.Result.FiredRules
	if firedRules == nil {
		firedRules = []scoring.FiredRule{}
	}
	platform.WriteJSON(w, http.StatusOK, scoreResponse{
		TransactionID:  transactionID,
		Outcome:        d.Result.Outcome,
		Score:          d.Result.Score,
		FiredRules:     firedRules,
		RulesetVersion: d.RulesetVersion,
		ModelScore:     d.ModelScore,
		ModelVersion:   d.ModelVersion,
	})
}

// decode reads exactly one JSON object into req, a pointer to a request
// struct. On failure it returns the status code and error response to send.
func decode(w http.ResponseWriter, r *http.Request, req any) (int, *platform.ErrorResponse) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

	err := dec.Decode(req)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("request body must contain a single JSON object")
	}
	if err == nil {
		return 0, nil
	}

	var (
		typeErr  *json.UnmarshalTypeError
		syntax   *json.SyntaxError
		tooLarge *http.MaxBytesError
		timeErr  *time.ParseError
	)
	switch {
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge, &platform.ErrorResponse{
			Error: fmt.Sprintf("request body must not exceed %d bytes", maxBodyBytes),
		}
	case errors.As(err, &typeErr):
		if typeErr.Field == "" {
			return http.StatusBadRequest, &platform.ErrorResponse{Error: "request body must be a JSON object"}
		}
		return http.StatusBadRequest, &platform.ErrorResponse{
			Error:  "invalid request",
			Fields: map[string]string{typeErr.Field: typeMessage(typeErr.Type)},
		}
	case errors.As(err, &timeErr), strings.Contains(err.Error(), "Time.UnmarshalJSON"):
		return http.StatusBadRequest, &platform.ErrorResponse{
			Error: "times must be RFC 3339, for example 2026-11-08T10:00:00Z",
		}
	case errors.As(err, &syntax), errors.Is(err, io.ErrUnexpectedEOF):
		return http.StatusBadRequest, &platform.ErrorResponse{Error: "request body is not valid JSON"}
	case errors.Is(err, io.EOF):
		return http.StatusBadRequest, &platform.ErrorResponse{Error: "request body is empty"}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return http.StatusBadRequest, &platform.ErrorResponse{
			Error:  "invalid request",
			Fields: map[string]string{field: "is not a known field"},
		}
	default:
		return http.StatusBadRequest, &platform.ErrorResponse{Error: err.Error()}
	}
}

func typeMessage(t reflect.Type) string {
	if t == reflect.TypeFor[time.Time]() {
		return "must be an RFC 3339 time, for example 2026-11-08T10:00:00Z"
	}
	switch t.Kind() {
	case reflect.Int64:
		return "must be an integer"
	case reflect.Float64:
		return "must be a number"
	case reflect.String:
		return "must be a string"
	default:
		return "has the wrong type"
	}
}

// purchase validates the request and converts it to a Purchase. It returns the
// problems found, keyed by field name. Empty optional strings count as omitted.
// Event times in the past are allowed, so recorded traffic can be replayed.
func (req scoreRequest) purchase(now time.Time) (models.Purchase, map[string]string) {
	fields := map[string]string{}
	required := func(name, value string) string {
		value = strings.TrimSpace(value)
		if value == "" {
			fields[name] = "is required"
		}
		return value
	}

	p := models.Purchase{
		TransactionID:        required("transaction_id", req.TransactionID),
		EventTime:            req.EventTime.UTC(),
		AmountMinor:          req.AmountMinor,
		PlayerID:             required("player_id", req.PlayerID),
		CardToken:            required("card_token", req.CardToken),
		CardIssuer:           optional(req.CardIssuer),
		CardNetwork:          optional(req.CardNetwork),
		CardType:             optional(req.CardType),
		CardCountry:          optional(req.CardCountry),
		CardCategory:         optional(req.CardCategory),
		ProductCode:          optional(req.ProductCode),
		BillingRegion:        optional(req.BillingRegion),
		BillingCountry:       optional(req.BillingCountry),
		BillingDistance:      req.BillingDistance,
		EmailDomain:          optional(req.EmailDomain),
		RecipientEmailDomain: optional(req.RecipientEmailDomain),
		DeviceType:           optional(req.DeviceType),
		DeviceName:           optional(req.DeviceName),
		DeviceID:             optional(req.DeviceID),
		IPCountry:            optional(req.IPCountry),
		ClientOS:             optional(req.ClientOS),
		ClientBrowser:        optional(req.ClientBrowser),
		ScreenResolution:     optional(req.ScreenResolution),
	}

	if req.EventTime.IsZero() {
		fields["event_time"] = "is required"
	} else if p.EventTime.Sub(now) > maxClockSkew {
		fields["event_time"] = fmt.Sprintf("must not be more than %d minutes in the future", int(maxClockSkew.Minutes()))
	}
	if req.AmountMinor <= 0 {
		fields["amount_minor"] = "must be a positive integer in minor units"
	}
	if p.BillingDistance != nil && *p.BillingDistance < 0 {
		fields["billing_distance"] = "must not be negative"
	}
	p.IPAddress = ipAddress(req.IPAddress, fields)
	p.AccountCreatedAt = req.accountCreatedAt(p.EventTime, fields)
	p.CVVResult, p.AVSResult, p.ThreeDSResult = req.verification(fields)
	return p, fields
}

// verification returns the optional gateway verification results and records
// a problem in fields for each value that is not a known result.
func (req scoreRequest) verification(fields map[string]string) (*scoring.CVVResult, *scoring.AVSResult, *scoring.ThreeDSResult) {
	return enumValue("cvv_result", req.CVVResult, scoring.CVVResults, fields),
		enumValue("avs_result", req.AVSResult, scoring.AVSResults, fields),
		enumValue("three_ds_result", req.ThreeDSResult, scoring.ThreeDSResults, fields)
}

// enumValue returns the optional value when it is one of allowed, and records
// a problem in fields when it is not. Values are compared exactly, after
// trimming, like the other optional strings.
func enumValue[T ~string](name string, s *string, allowed []T, fields map[string]string) *T {
	text := optional(s)
	if text == nil {
		return nil
	}
	value := T(*text)
	if !slices.Contains(allowed, value) {
		fields[name] = "must be " + choiceList(allowed)
		return nil
	}
	return &value
}

// choiceList writes the values as "a, b, or c".
func choiceList[T ~string](values []T) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = string(v)
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
}

// ipAddress parses the optional IP address and records a problem in fields.
// An IPv4 address written in IPv6 form is returned as IPv4.
func ipAddress(s *string, fields map[string]string) *netip.Addr {
	ip := optional(s)
	if ip == nil {
		return nil
	}
	addr, err := netip.ParseAddr(*ip)
	if err != nil {
		fields["ip_address"] = "must be an IPv4 or IPv6 address"
		return nil
	}
	addr = addr.Unmap()
	return &addr
}

// accountCreatedAt returns the optional account creation time in UTC and
// records a problem in fields when it is after the event time.
func (req scoreRequest) accountCreatedAt(eventTime time.Time, fields map[string]string) *time.Time {
	if req.AccountCreatedAt == nil {
		return nil
	}
	created := req.AccountCreatedAt.UTC()
	if !req.EventTime.IsZero() && created.After(eventTime) {
		fields["account_created_at"] = "must not be after event_time"
	}
	return &created
}

func optional(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}
