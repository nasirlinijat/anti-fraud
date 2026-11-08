package score

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const maxBodyBytes = 64 << 10

// Rulesets provides the active ruleset.
type Rulesets interface {
	Active(ctx context.Context) (scoring.Ruleset, error)
}

// Purchases reads purchase history and saves scored purchases.
type Purchases interface {
	CardPurchasesLastHour(ctx context.Context, cardToken string, at time.Time) (int, error)
	Save(ctx context.Context, p Purchase, d Decision) (int64, error)
}

// Handler serves the score endpoint.
type Handler struct {
	rulesets  Rulesets
	purchases Purchases
}

func NewHandler(rulesets Rulesets, purchases Purchases) *Handler {
	return &Handler{rulesets: rulesets, purchases: purchases}
}

// RegisterRoutes registers POST /score behind API key authentication.
func RegisterRoutes(mux *http.ServeMux, h *Handler, apiKey string) {
	mux.Handle("POST /score", platform.RequireAPIKey(apiKey, h))
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
}

type scoreResponse struct {
	TransactionID  string              `json:"transaction_id"`
	Outcome        scoring.Outcome     `json:"outcome"`
	Score          int                 `json:"score"`
	FiredRules     []scoring.FiredRule `json:"fired_rules"`
	RulesetVersion int                 `json:"ruleset_version"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req scoreRequest
	if status, resp := decode(w, r, &req); resp != nil {
		platform.WriteJSON(w, status, resp)
		return
	}
	p, fields := req.purchase()
	if len(fields) > 0 {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: fields})
		return
	}

	ctx := r.Context()
	rs, err := h.rulesets.Active(ctx)
	if err != nil {
		h.internalError(w, p, err)
		return
	}
	cardUses, err := h.purchases.CardPurchasesLastHour(ctx, p.CardToken, p.EventTime)
	if err != nil {
		h.internalError(w, p, err)
		return
	}

	in := scoring.Input{
		AmountMinor:           p.AmountMinor,
		IPCountry:             p.IPCountry,
		AccountCreatedAt:      p.AccountCreatedAt,
		EventTime:             p.EventTime,
		CardPurchasesLastHour: cardUses,
	}
	if p.CardCountry != nil {
		in.CardCountry = *p.CardCountry
	}
	result := scoring.Evaluate(in, rs)
	if result.FiredRules == nil {
		result.FiredRules = []scoring.FiredRule{}
	}

	_, err = h.purchases.Save(ctx, p, Decision{
		RulesetVersion: rs.Version,
		Result:         result,
		ModelInputs:    map[string]float64{},
	})
	if errors.Is(err, ErrDuplicateTransaction) {
		platform.WriteJSON(w, http.StatusConflict, platform.ErrorResponse{
			Error:  "a purchase with this transaction ID was already scored",
			Fields: map[string]string{"transaction_id": "is already scored"},
		})
		return
	}
	if err != nil {
		h.internalError(w, p, err)
		return
	}

	platform.WriteJSON(w, http.StatusOK, scoreResponse{
		TransactionID:  p.TransactionID,
		Outcome:        result.Outcome,
		Score:          result.Score,
		FiredRules:     result.FiredRules,
		RulesetVersion: rs.Version,
	})
}

func (h *Handler) internalError(w http.ResponseWriter, p Purchase, err error) {
	log.Printf("score purchase %s: %v", p.TransactionID, err)
	platform.WriteError(w, http.StatusInternalServerError, "internal error")
}

// decode reads exactly one JSON object into req. On failure it returns the
// status code and error response to send.
func decode(w http.ResponseWriter, r *http.Request, req *scoreRequest) (int, *platform.ErrorResponse) {
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
func (req scoreRequest) purchase() (Purchase, map[string]string) {
	fields := map[string]string{}
	required := func(name, value string) string {
		value = strings.TrimSpace(value)
		if value == "" {
			fields[name] = "is required"
		}
		return value
	}

	p := Purchase{
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
	}

	if req.EventTime.IsZero() {
		fields["event_time"] = "is required"
	}
	if req.AmountMinor <= 0 {
		fields["amount_minor"] = "must be a positive integer in minor units"
	}
	if p.BillingDistance != nil && *p.BillingDistance < 0 {
		fields["billing_distance"] = "must not be negative"
	}
	if ip := optional(req.IPAddress); ip != nil {
		addr, err := netip.ParseAddr(*ip)
		if err != nil {
			fields["ip_address"] = "must be an IPv4 or IPv6 address"
		} else {
			addr = addr.Unmap()
			p.IPAddress = &addr
		}
	}
	if req.AccountCreatedAt != nil {
		created := req.AccountCreatedAt.UTC()
		if !req.EventTime.IsZero() && created.After(p.EventTime) {
			fields["account_created_at"] = "must not be after event_time"
		}
		p.AccountCreatedAt = &created
	}
	return p, fields
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
