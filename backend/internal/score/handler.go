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

// maxClockSkew is how far in the future an event time may be. It allows for
// clocks that are slightly ahead; purchases further ahead would break the
// feature running totals, which assume purchases arrive in time order.
const maxClockSkew = 5 * time.Minute

// Rulesets provides the active ruleset.
type Rulesets interface {
	Active(ctx context.Context) (scoring.Ruleset, error)
}

// Blocklist finds the blocklist entry that matches a purchase.
type Blocklist interface {
	Match(ctx context.Context, s blocklist.Subject) (blocklist.Entry, bool, error)
}

// Purchases saves decisions and looks up saved decisions and purchase history.
type Purchases interface {
	FindSavedDecision(ctx context.Context, p Purchase) (*Decision, error)
	CardPurchasesLastHour(ctx context.Context, cardToken string, at time.Time) (int, error)
	DeviceCardsLastHour(ctx context.Context, deviceID, cardToken string, at time.Time) (int64, error)
	TrustedHistory(ctx context.Context, p Purchase, windowDays int32) (*scoring.TrustedPurchaseHistory, error)
	FeatureHistory(ctx context.Context, in features.Input) (features.History, error)
	PlayerEarlierModelScores(ctx context.Context, playerID string, at time.Time, transactionID string) (float64, int64, error)
	Save(ctx context.Context, p Purchase, d Decision) (int64, error)
}

// Model scores a feature vector from 0 (legitimate) to 1 (fraud).
type Model interface {
	Predict(x []float64) (float64, error)
	Features() []string
	Version() string
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
	// modelFeatures is how many of the first features in features.Names the
	// model takes.
	modelFeatures int
	policy        scoring.ModelPolicy
	now           func() time.Time
}

func NewHandler(rulesets Rulesets, blocklist Blocklist, purchases Purchases) *Handler {
	return &Handler{rulesets: rulesets, blocklist: blocklist, purchases: purchases, now: time.Now}
}

// UseModel makes the handler score purchases with the model and apply its
// policy to the rule outcome. Without a model the handler uses rules only. The
// model must take the first features of features.Names, in their order.
// Features are only ever added at the end, so a model exported before the
// newest features still loads and is scored without them.
func (h *Handler) UseModel(m Model, p scoring.ModelPolicy) error {
	got := m.Features()
	if len(got) == 0 || len(got) > len(features.Names) || !slices.Equal(got, features.Names[:len(got)]) {
		return fmt.Errorf("model features %v are not the first features of the service %v", got, features.Names)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	h.model, h.modelFeatures, h.policy = m, len(got), p
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
	ModelVersion   *string             `json:"model_version"`
}

// ServeHTTP scores one purchase and returns its saved decision. A retry of a
// saved transaction returns the decision saved the first time.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	ctx := r.Context()
	if h.respondWithSavedDecision(w, ctx, p) {
		return
	}
	decision, err := h.decide(ctx, p)
	if err != nil {
		h.internalError(w, p, err)
		return
	}
	decision.Latency = time.Since(started)
	h.saveAndRespond(w, ctx, p, decision)
}

// decide scores a purchase that has no saved decision. The caller sets the
// latency.
func (h *Handler) decide(ctx context.Context, p Purchase) (Decision, error) {
	rs, err := h.rulesets.Active(ctx)
	if err != nil {
		return Decision{}, err
	}

	// A blocklist match blocks the purchase before the rules run.
	entry, blocked, err := h.blocklist.Match(ctx, blocklist.Subject{
		CardToken: p.CardToken,
		PlayerID:  p.PlayerID,
		DeviceID:  p.DeviceID,
		IPAddress: p.IPAddress,
	})
	if err != nil {
		return Decision{}, err
	}

	// Model inputs are computed for every purchase, blocked ones too, so that
	// all saved purchases can be exported for training.
	in := p.FeatureInput()
	history, err := h.purchases.FeatureHistory(ctx, in)
	if err != nil {
		return Decision{}, err
	}
	vector := features.Vector(in, history)
	modelInputs := features.Map(vector)
	model, err := h.runModel(ctx, p, rs, vector)
	if err != nil {
		return Decision{}, err
	}

	var result scoring.Result
	if blocked {
		result = scoring.Result{Outcome: scoring.OutcomeBlock, FiredRules: []scoring.FiredRule{scoring.BlocklistMatch(string(entry.Type))}}
	} else {
		result, err = h.evaluateRules(ctx, p, rs, model.score)
		if err != nil {
			return Decision{}, err
		}
	}
	return Decision{
		RulesetVersion:      rs.Version,
		Result:              result,
		ModelInputs:         modelInputs,
		ModelScore:          model.score,
		ModelScoreUnblended: model.unblended,
		ModelVersion:        model.version,
		FeatureHistory:      history,
	}, nil
}

// modelResult is the model's part of a decision. Its fields are nil when the
// model did not run.
type modelResult struct {
	score, unblended *float64
	version          *string
}

// runModel scores the purchase with the model. The ruleset's model mode
// decides whether the model runs; whether its score can change the outcome is
// decided in evaluateRules. Without a model the rules decide alone.
func (h *Handler) runModel(ctx context.Context, p Purchase, rs scoring.Ruleset, vector []float64) (modelResult, error) {
	scoreWithModel := h.model != nil && (rs.Model.Mode == scoring.ModelShadow || rs.Model.Mode == scoring.ModelAssist)
	if !scoreWithModel {
		return modelResult{}, nil
	}
	final, own, err := h.modelScore(ctx, p, vector)
	if err != nil {
		return modelResult{}, err
	}
	version := h.model.Version()
	return modelResult{score: &final, unblended: &own, version: &version}, nil
}

// evaluateRules runs the rules on a purchase that is not blocklisted. In
// assist mode the model score then adjusts the outcome. modelScore is nil when
// the model did not run.
func (h *Handler) evaluateRules(ctx context.Context, p Purchase, rs scoring.Ruleset, modelScore *float64) (scoring.Result, error) {
	in, err := h.ruleInput(ctx, p, rs)
	if err != nil {
		return scoring.Result{}, err
	}
	result := scoring.Evaluate(in, rs)
	if modelScore != nil && rs.Model.Mode == scoring.ModelAssist {
		result.Outcome = scoring.ApplyModel(result.Outcome, *modelScore, rs.Model.Policy(h.policy))
	}
	return result, nil
}

// ruleInput builds the rule input. It reads only the purchase history that
// the enabled rules need.
func (h *Handler) ruleInput(ctx context.Context, p Purchase, rs scoring.Ruleset) (scoring.Input, error) {
	cardUses, err := h.purchases.CardPurchasesLastHour(ctx, p.CardToken, p.EventTime)
	if err != nil {
		return scoring.Input{}, err
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
	if p.DeviceID != nil && rs.Rules[scoring.RuleDeviceCardCount].Enabled {
		in.DeviceCardsLastHour, err = h.purchases.DeviceCardsLastHour(ctx, *p.DeviceID, p.CardToken, p.EventTime)
		if err != nil {
			return scoring.Input{}, err
		}
	}
	spike := rs.Rules[scoring.RuleNewDeviceSpike]
	if p.DeviceID != nil && spike.Enabled && p.AmountMinor >= spike.MinAmountMinor {
		in.TrustedHistory, err = h.purchases.TrustedHistory(ctx, p, spike.WindowDays)
		if err != nil {
			return scoring.Input{}, err
		}
	}
	return in, nil
}

// saveAndRespond saves the decision and writes the response. When another
// request saved the same transaction first, it responds with that decision.
func (h *Handler) saveAndRespond(w http.ResponseWriter, ctx context.Context, p Purchase, decision Decision) {
	_, err := h.purchases.Save(ctx, p, decision)
	if errors.Is(err, ErrDuplicateTransaction) {
		// Save has rolled back, so the lookup can see the committed winner.
		if !h.respondWithSavedDecision(w, ctx, p) {
			h.internalError(w, p, errors.New("saved decision missing after duplicate transaction"))
		}
		return
	}
	if err != nil {
		h.internalError(w, p, err)
		return
	}
	writeScoreResponse(w, p.TransactionID, decision)
}

// respondWithSavedDecision writes the response for a saved transaction or a
// lookup error. It returns false when there is no saved decision.
func (h *Handler) respondWithSavedDecision(w http.ResponseWriter, ctx context.Context, p Purchase) bool {
	decision, err := h.purchases.FindSavedDecision(ctx, p)
	switch {
	case errors.Is(err, ErrDuplicateTransaction):
		platform.WriteJSON(w, http.StatusConflict, platform.ErrorResponse{
			Error:  "a purchase with this transaction ID was already scored",
			Fields: map[string]string{"transaction_id": "is already scored"},
		})
	case err != nil:
		h.internalError(w, p, err)
	case decision == nil:
		return false
	default:
		writeScoreResponse(w, p.TransactionID, *decision)
	}
	return true
}

func writeScoreResponse(w http.ResponseWriter, transactionID string, d Decision) {
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

// modelScore returns the score the policy applies to, and the score saved as
// unblended: the main model's own score, which later purchases of the player
// average. Without blending both come from the model alone.
func (h *Handler) modelScore(ctx context.Context, p Purchase, vector []float64) (float64, float64, error) {
	vector = vector[:h.modelFeatures]
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
// Event times in the past are allowed, so recorded traffic can be replayed.
func (req scoreRequest) purchase(now time.Time) (Purchase, map[string]string) {
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
	return p, fields
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
