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
	"slices"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const maxBodyBytes = 64 << 10

// Rulesets provides the active ruleset.
type Rulesets interface {
	Active(ctx context.Context) (scoring.Ruleset, error)
}

// Blocklist finds the blocklist entry that matches a purchase.
type Blocklist interface {
	Match(ctx context.Context, s blocklist.Subject) (blocklist.Entry, bool, error)
}

// Purchases reads purchase history and saves scored purchases.
type Purchases interface {
	CardPurchasesLastHour(ctx context.Context, cardToken string, at time.Time) (int, error)
	FeatureHistory(ctx context.Context, in features.Input) (features.History, error)
	PlayerEarlierModelScores(ctx context.Context, playerID string, at time.Time, transactionID string) (float64, int64, error)
	Save(ctx context.Context, p Purchase, d Decision) (int64, error)
}

// Model scores a feature vector from 0 (legitimate) to 1 (fraud).
type Model interface {
	Predict(x []float64) (float64, error)
	Features() []string
}

// combination is a model made of several models, such as *lgbm.Combined. The
// player blend applies to its first model's score, before combining.
type combination interface {
	Scores(x []float64) ([]float64, error)
	Combine(scores []float64) float64
}

// Handler serves the score endpoint.
type Handler struct {
	rulesets  Rulesets
	blocklist Blocklist
	purchases Purchases
	model     Model
	policy    scoring.ModelPolicy
}

func NewHandler(rulesets Rulesets, blocklist Blocklist, purchases Purchases) *Handler {
	return &Handler{rulesets: rulesets, blocklist: blocklist, purchases: purchases}
}

// UseModel makes the handler score purchases with the model and apply its
// policy to the rule outcome. Without a model the handler uses rules only. The
// model must take the features in the order of features.Names.
func (h *Handler) UseModel(m Model, p scoring.ModelPolicy) error {
	if got := m.Features(); !slices.Equal(got, features.Names) {
		return fmt.Errorf("model features %v do not match the service features %v", got, features.Names)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	h.model, h.policy = m, p
	return nil
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
	ClientOS             *string    `json:"client_os"`
	ClientBrowser        *string    `json:"client_browser"`
	ScreenResolution     *string    `json:"screen_resolution"`
}

type scoreResponse struct {
	TransactionID  string              `json:"transaction_id"`
	Outcome        scoring.Outcome     `json:"outcome"`
	Score          int                 `json:"score"`
	FiredRules     []scoring.FiredRule `json:"fired_rules"`
	RulesetVersion int                 `json:"ruleset_version"`
	ModelScore     *float64            `json:"model_score"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
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

	// A blocklist match blocks the purchase before the rules run.
	_, blocked, err := h.blocklist.Match(ctx, blocklist.Subject{
		CardToken: p.CardToken,
		PlayerID:  p.PlayerID,
		DeviceID:  p.DeviceID,
		IPAddress: p.IPAddress,
	})
	if err != nil {
		h.internalError(w, p, err)
		return
	}

	// Model inputs are computed for every purchase, blocked ones too, so that
	// all saved purchases can be exported for training.
	in := p.FeatureInput()
	history, err := h.purchases.FeatureHistory(ctx, in)
	if err != nil {
		h.internalError(w, p, err)
		return
	}
	vector := features.Vector(in, history)
	modelInputs := features.Map(vector)

	var modelScore, unblended *float64
	if h.model != nil {
		final, own, err := h.modelScore(r.Context(), p, vector)
		if err != nil {
			h.internalError(w, p, err)
			return
		}
		modelScore, unblended = &final, &own
	}

	var result scoring.Result
	if blocked {
		result = scoring.Result{Outcome: scoring.OutcomeBlock}
	} else {
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
		result = scoring.Evaluate(in, rs)
		if modelScore != nil {
			result.Outcome = scoring.ApplyModel(result.Outcome, *modelScore, h.policy)
		}
	}
	if result.FiredRules == nil {
		result.FiredRules = []scoring.FiredRule{}
	}

	_, err = h.purchases.Save(ctx, p, Decision{
		RulesetVersion:      rs.Version,
		Result:              result,
		ModelInputs:         modelInputs,
		ModelScore:          modelScore,
		ModelScoreUnblended: unblended,
		Latency:             time.Since(started),
		FeatureHistory:      history,
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
		ModelScore:     modelScore,
	})
}

// modelScore returns the score the policy applies to, and the score saved as
// unblended: the main model's own score, which later purchases of the player
// average. Without blending both come from the model alone.
func (h *Handler) modelScore(ctx context.Context, p Purchase, vector []float64) (float64, float64, error) {
	combined, isCombined := h.model.(combination)
	if !isCombined {
		own, err := h.model.Predict(vector)
		if err != nil {
			return 0, 0, err
		}
		if h.policy.PlayerBlendWeight == 0 {
			return own, own, nil
		}
		sum, count, err := h.purchases.PlayerEarlierModelScores(ctx, p.PlayerID, p.EventTime, p.TransactionID)
		if err != nil {
			return 0, 0, err
		}
		return h.policy.Blend(own, sum, count), own, nil
	}

	scores, err := combined.Scores(vector)
	if err != nil {
		return 0, 0, err
	}
	own := scores[0]
	if h.policy.PlayerBlendWeight > 0 {
		sum, count, err := h.purchases.PlayerEarlierModelScores(ctx, p.PlayerID, p.EventTime, p.TransactionID)
		if err != nil {
			return 0, 0, err
		}
		scores[0] = h.policy.Blend(own, sum, count)
	}
	return combined.Combine(scores), own, nil
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
		ClientOS:             optional(req.ClientOS),
		ClientBrowser:        optional(req.ClientBrowser),
		ScreenResolution:     optional(req.ScreenResolution),
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
