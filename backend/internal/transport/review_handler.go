package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
)

const maxDecisionBytes = 1024

type ReviewService interface {
	Queue(ctx context.Context, order models.QueueOrder, after *models.QueueCursor, claimedBy *string) (review.Page, error)
	QueuePage(ctx context.Context, order models.QueueOrder, number int64, claimedBy *string) (models.NumberedReviewPage, error)
	Case(ctx context.Context, purchaseID int64) (models.CaseItem, error)
	ResolvedCase(ctx context.Context, purchaseID int64) (models.CaseItem, error)
	PurchaseCase(ctx context.Context, purchaseID int64) (models.PurchaseCase, error)
	SearchPurchases(ctx context.Context, query string) ([]models.PurchaseMatch, error)
	Network(ctx context.Context, purchaseID int64) (models.CaseNetwork, error)
	NetworkPurchases(ctx context.Context, purchaseID int64, anchor models.BlocklistType, value string) (models.LinkedPurchases, error)
	LinkedReviews(ctx context.Context, purchaseID int64) (models.LinkedReviews, error)
	Decide(ctx context.Context, purchaseID int64, decision, analystID string, blocks models.ReviewBlocks) (models.Resolution, error)
	Claim(ctx context.Context, purchaseID int64, username string) (models.Claim, error)
	ClaimMany(ctx context.Context, purchaseIDs []int64, username string) ([]review.ClaimResult, error)
	DecideMany(ctx context.Context, d review.BatchDecision) ([]review.DecisionResult, error)
	Assignments(ctx context.Context) ([]models.ReviewerWork, error)
	ReleaseClaim(ctx context.Context, purchaseID int64) error
}

type ReviewHandler struct {
	service      ReviewService
	modelEnabled bool
	explainer    ModelExplainer
}

func NewReviewHandler(service ReviewService, modelEnabled bool) *ReviewHandler {
	return &ReviewHandler{service: service, modelEnabled: modelEnabled}
}

func (h *ReviewHandler) UseExplainer(explainer ModelExplainer) *ReviewHandler {
	h.explainer = explainer
	return h
}

// RegisterReviewRoutes exposes the queue, case evidence, claims, and decisions
// to signed-in reviewers and admins. Only
// signed-in accounts can claim, and they decide only what they have claimed.
// The batch routes claim and decide several purchases, each on its own.
func RegisterReviewRoutes(mux *http.ServeMux, h *ReviewHandler, access platform.Access) {
	mux.HandleFunc("GET /reviews", access.Review(h.serveQueue))
	mux.HandleFunc("GET /reviews/{purchase_id}", access.Review(func(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
		h.serveCase(w, r)
	}))
	mux.HandleFunc("GET /purchases/search", access.Review(func(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
		h.serveSearch(w, r)
	}))
	mux.HandleFunc("GET /purchases/{purchase_id}", access.Review(func(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
		h.servePurchase(w, r)
	}))
	mux.HandleFunc("GET /reviews/{purchase_id}/resolved", access.Review(func(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
		h.serveResolvedCase(w, r)
	}))
	mux.HandleFunc("GET /reviews/{purchase_id}/network", access.Review(func(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
		h.serveNetwork(w, r)
	}))
	mux.HandleFunc("GET /reviews/{purchase_id}/network/purchases", access.Review(func(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
		h.serveNetworkPurchases(w, r)
	}))
	mux.HandleFunc("GET /reviews/{purchase_id}/network/reviews", access.Review(func(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
		h.serveLinkedReviews(w, r)
	}))
	mux.HandleFunc("POST /reviews/{purchase_id}/claim", access.Review(h.serveClaim))
	mux.HandleFunc("POST /reviews/{purchase_id}/decision", access.Review(h.serveDecision))
	mux.HandleFunc("POST /reviews/claims", access.Review(h.serveBatchClaim))
	mux.HandleFunc("POST /reviews/decisions", access.Review(h.serveBatchDecision))
	mux.HandleFunc("DELETE /reviews/{purchase_id}/claim", access.Admin(h.serveRelease))
	mux.HandleFunc("GET /admin/assignments", access.Admin(h.serveAssignments))
}

type reviewResponse struct {
	PurchaseID    string              `json:"purchase_id"`
	TransactionID string              `json:"transaction_id"`
	EventTime     time.Time           `json:"event_time"`
	QueuedAt      time.Time           `json:"queued_at"`
	AmountMinor   string              `json:"amount_minor"`
	PlayerID      string              `json:"player_id"`
	DeviceID      *string             `json:"device_id"`
	RuleScore     int                 `json:"rule_score"`
	ModelScore    *float64            `json:"model_score"`
	ModelVersion  *string             `json:"model_version"`
	FiredRules    []scoring.FiredRule `json:"fired_rules"`
	ClaimedBy     *string             `json:"claimed_by"`
	ClaimedAt     *time.Time          `json:"claimed_at"`
}

type playerPurchaseResponse struct {
	TransactionID string          `json:"transaction_id"`
	EventTime     time.Time       `json:"event_time"`
	AmountMinor   string          `json:"amount_minor"`
	Outcome       scoring.Outcome `json:"outcome"`
}

type deviceActivityResponse struct {
	Purchases     int64     `json:"purchases"`
	DistinctCards int64     `json:"distinct_cards"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

// caseAccountEventResponse is a login or password reset of the player before
// the purchase under review. RecordedAfterDecision is true for an event the
// service recorded after the purchase was decided, which the rules could not
// have seen.
type caseAccountEventResponse struct {
	EventID               string                  `json:"event_id"`
	EventType             models.AccountEventType `json:"event_type"`
	EventTime             time.Time               `json:"event_time"`
	DeviceID              *string                 `json:"device_id"`
	IPCountry             *string                 `json:"ip_country"`
	ReceivedAt            time.Time               `json:"received_at"`
	RecordedAfterDecision bool                    `json:"recorded_after_decision"`
}

type caseResponse struct {
	reviewResponse
	RulesetVersion  int                        `json:"ruleset_version"`
	PlayerPurchases []playerPurchaseResponse   `json:"player_purchases"`
	DeviceActivity  *deviceActivityResponse    `json:"device_activity"`
	CVVResult       *scoring.CVVResult         `json:"cvv_result"`
	AVSResult       *scoring.AVSResult         `json:"avs_result"`
	ThreeDSResult   *scoring.ThreeDSResult     `json:"three_ds_result"`
	AccountEvents   []caseAccountEventResponse `json:"account_events"`
	IPIntel         *ipIntelResponse           `json:"ip_intel"`
	ModelReasons    []modelReasonResponse      `json:"model_reasons"`
	CardVelocity    *cardVelocityResponse      `json:"card_velocity_evidence,omitempty"`
}

type modelReasonResponse struct {
	Feature string  `json:"feature"`
	Value   float64 `json:"value"`
	Effect  float64 `json:"effect"`
}

const modelReasonCount = 5

func (h *ReviewHandler) modelReasons(ctx context.Context, item models.CaseItem) []modelReasonResponse {
	reasons := []modelReasonResponse{}
	if h.explainer == nil || item.ModelScore == nil || item.ModelVersion == nil || *item.ModelVersion != h.explainer.Version() || len(item.ModelInputs) == 0 {
		return reasons
	}
	explained, err := h.explainer.Explain(features.VectorFrom(item.ModelInputs, h.explainer.Features()), modelReasonCount)
	if err != nil {
		slog.WarnContext(ctx, "explain model score", "purchase_id", item.PurchaseID, "error", err)
		return reasons
	}
	for _, reason := range explained {
		reasons = append(reasons, modelReasonResponse{Feature: reason.Feature, Value: reason.Value, Effect: reason.Effect})
	}
	return reasons
}

// ipIntelResponse is the IP intelligence lookup saved with the decision.
type ipIntelResponse struct {
	Provider  string `json:"provider"`
	Available bool   `json:"available"`
	Hosting   bool   `json:"hosting"`
	VPN       bool   `json:"vpn"`
	Proxy     bool   `json:"proxy"`
	Tor       bool   `json:"tor"`
}

type networkResponse struct {
	WindowDays int                     `json:"window_days"`
	Anchors    []networkAnchorResponse `json:"anchors"`
}

type networkAnchorResponse struct {
	Type        models.BlocklistType   `json:"type"`
	Value       string                 `json:"value"`
	Blocklisted bool                   `json:"blocklisted"`
	LinkedTotal int64                  `json:"linked_total"`
	Linked      []linkedEntityResponse `json:"linked"`
}

type linkedEntityResponse struct {
	Type      models.BlocklistType `json:"type"`
	Value     string               `json:"value"`
	Purchases int64                `json:"purchases"`
	Status    models.NetworkStatus `json:"status"`
}

type queueResponse struct {
	Items        []reviewResponse `json:"items"`
	NextCursor   *string          `json:"next_cursor"`
	ModelEnabled bool             `json:"model_enabled"`
	Pagination   *queuePagination `json:"pagination,omitempty"`
}

type queuePagination struct {
	Page       int64 `json:"page"`
	TotalPages int64 `json:"total_pages"`
	TotalItems int64 `json:"total_items"`
}

type decisionResponse struct {
	PurchaseID string    `json:"purchase_id"`
	Decision   string    `json:"decision"`
	AnalystID  string    `json:"analyst_id"`
	ResolvedAt time.Time `json:"resolved_at"`
	// The blocked fields are true when this rejection added that value of the
	// purchase to the blocklist.
	DeviceBlocked bool `json:"device_blocked"`
	CardBlocked   bool `json:"card_blocked"`
	PlayerBlocked bool `json:"player_blocked"`
}

type claimResponse struct {
	PurchaseID string    `json:"purchase_id"`
	ClaimedBy  string    `json:"claimed_by"`
	ClaimedAt  time.Time `json:"claimed_at"`
}

type openClaimResponse struct {
	PurchaseID  string    `json:"purchase_id"`
	AmountMinor string    `json:"amount_minor"`
	RuleScore   int       `json:"rule_score"`
	ModelScore  *float64  `json:"model_score"`
	ClaimedAt   time.Time `json:"claimed_at"`
}

type reviewerWorkResponse struct {
	Username   string              `json:"username"`
	Role       models.Role         `json:"role"`
	Disabled   bool                `json:"disabled"`
	Decided    int64               `json:"decided"`
	OpenClaims []openClaimResponse `json:"open_claims"`
}

type assignmentsResponse struct {
	Reviewers []reviewerWorkResponse `json:"reviewers"`
}

// decisionRequest is the body of a review decision.
type decisionRequest struct {
	Decision    string `json:"decision"`
	BlockDevice bool   `json:"block_device"`
	BlockCard   bool   `json:"block_card"`
	BlockPlayer bool   `json:"block_player"`
}

func (body decisionRequest) blocks() models.ReviewBlocks {
	return models.ReviewBlocks{Device: body.BlockDevice, Card: body.BlockCard, Player: body.BlockPlayer}
}

func (h *ReviewHandler) serveQueue(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid query parameters")
		return
	}
	order := models.QueueByRuleScore
	if values, present := params["sort"]; present {
		if len(values) != 1 || (models.QueueOrder(values[0]) != models.QueueByRuleScore && models.QueueOrder(values[0]) != models.QueueByModelScore) {
			platform.WriteError(w, http.StatusBadRequest, "invalid sort order")
			return
		}
		order = models.QueueOrder(values[0])
	}
	after, err := parseCursor(params["cursor"], order)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	claimedBy, err := claimFilter(params["claimed"], actor)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if values, numbered := params["page"]; numbered {
		number, err := parseQueuePage(values)
		if err != nil || after != nil {
			platform.WriteError(w, http.StatusBadRequest, "page must be a positive integer and cannot be combined with cursor")
			return
		}
		h.serveNumberedQueue(w, r, order, number, claimedBy)
		return
	}
	h.serveCursorQueue(w, r, order, after, claimedBy)
}

func parseQueuePage(values []string) (int64, error) {
	if len(values) == 1 {
		number, err := strconv.ParseInt(values[0], 10, 64)
		if err == nil && number > 0 {
			return number, nil
		}
	}
	return 0, errors.New("invalid page number")
}

func (h *ReviewHandler) serveNumberedQueue(w http.ResponseWriter, r *http.Request, order models.QueueOrder, number int64, claimedBy *string) {
	page, err := h.service.QueuePage(r.Context(), order, number, claimedBy)
	if err != nil {
		platform.WriteServerError(r.Context(), w, "read numbered review queue", err)
		return
	}
	response := queueResponse{
		Items: reviewItems(page.Items), ModelEnabled: h.modelEnabled,
		Pagination: &queuePagination{Page: page.Number, TotalPages: models.ReviewPageCount(page.TotalItems, review.PageSize), TotalItems: page.TotalItems},
	}
	platform.WriteJSON(w, http.StatusOK, response)
}

func (h *ReviewHandler) serveCursorQueue(w http.ResponseWriter, r *http.Request, order models.QueueOrder, after *models.QueueCursor, claimedBy *string) {
	page, err := h.service.Queue(r.Context(), order, after, claimedBy)
	if err != nil {
		platform.WriteServerError(r.Context(), w, "read review queue", err)
		return
	}

	var nextCursor *string
	if page.Next != nil {
		token := encodeCursor(order, *page.Next)
		nextCursor = &token
	}
	platform.WriteJSON(w, http.StatusOK, queueResponse{Items: reviewItems(page.Items), NextCursor: nextCursor, ModelEnabled: h.modelEnabled})
}

func reviewItems(rows []models.QueueItem) []reviewResponse {
	items := make([]reviewResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, newReviewResponse(row))
	}
	return items
}

func (h *ReviewHandler) serveCase(w http.ResponseWriter, r *http.Request) {
	readReviewCase(w, r, h.service.Case, h.modelReasons)
}

func (h *ReviewHandler) serveResolvedCase(w http.ResponseWriter, r *http.Request) {
	readReviewCase(w, r, h.service.ResolvedCase, h.modelReasons)
}

func readReviewCase(w http.ResponseWriter, r *http.Request, load func(context.Context, int64) (models.CaseItem, error), explain func(context.Context, models.CaseItem) []modelReasonResponse) {
	purchaseID, err := strconv.ParseInt(r.PathValue("purchase_id"), 10, 64)
	if err != nil || purchaseID <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	item, err := load(r.Context(), purchaseID)
	if errors.Is(err, models.ErrReviewNotFound) {
		platform.WriteError(w, http.StatusNotFound, "review case not found")
		return
	}
	if err != nil {
		platform.WriteServerError(r.Context(), w, "read review case", err, "purchase_id", purchaseID)
		return
	}
	response := newCaseResponse(item)
	response.ModelReasons = explain(r.Context(), item)
	if item.Resolution == nil {
		platform.WriteJSON(w, http.StatusOK, response)
		return
	}
	platform.WriteJSON(w, http.StatusOK, resolvedCaseResponse{
		caseResponse: response,
		Resolution: caseResolutionResponse{
			Decision: item.Resolution.Decision, AnalystID: item.Resolution.AnalystID, ResolvedAt: item.Resolution.ResolvedAt.UTC(),
		},
	})
}

type resolvedCaseResponse struct {
	caseResponse
	Resolution caseResolutionResponse `json:"resolution"`
}

type caseResolutionResponse struct {
	Decision   string    `json:"decision"`
	AnalystID  string    `json:"analyst_id"`
	ResolvedAt time.Time `json:"resolved_at"`
}

func newCaseResponse(item models.CaseItem) caseResponse {
	playerPurchases := make([]playerPurchaseResponse, 0, len(item.PlayerPurchases))
	for _, p := range item.PlayerPurchases {
		playerPurchases = append(playerPurchases, playerPurchaseResponse{
			TransactionID: p.TransactionID,
			EventTime:     p.EventTime.UTC(),
			AmountMinor:   strconv.FormatInt(p.AmountMinor, 10),
			Outcome:       p.Outcome,
		})
	}
	var deviceActivity *deviceActivityResponse
	if item.DeviceActivity != nil {
		deviceActivity = &deviceActivityResponse{
			Purchases:     item.DeviceActivity.Purchases,
			DistinctCards: item.DeviceActivity.DistinctCards,
			LastSeenAt:    item.DeviceActivity.LastSeenAt.UTC(),
		}
	}
	return caseResponse{
		reviewResponse:  newReviewResponse(item.QueueItem),
		RulesetVersion:  item.RulesetVersion,
		PlayerPurchases: playerPurchases,
		DeviceActivity:  deviceActivity,
		CVVResult:       item.CVVResult,
		AVSResult:       item.AVSResult,
		ThreeDSResult:   item.ThreeDSResult,
		AccountEvents:   newCaseAccountEvents(item.AccountEvents, item.DecidedAt),
		IPIntel:         newIPIntelResponse(item.IPIntel),
		CardVelocity:    newCardVelocityResponse(item.CardVelocity),
	}
}

func newCaseAccountEvents(events []models.AccountEvent, decidedAt time.Time) []caseAccountEventResponse {
	resp := make([]caseAccountEventResponse, 0, len(events))
	for _, e := range events {
		resp = append(resp, caseAccountEventResponse{
			EventID:               e.EventID,
			EventType:             e.EventType,
			EventTime:             e.EventTime.UTC(),
			DeviceID:              e.DeviceID,
			IPCountry:             e.IPCountry,
			ReceivedAt:            e.ReceivedAt.UTC(),
			RecordedAfterDecision: e.ReceivedAt.After(decidedAt),
		})
	}
	return resp
}

func newIPIntelResponse(r *ipintel.Result) *ipIntelResponse {
	if r == nil {
		return nil
	}
	return &ipIntelResponse{Provider: r.Provider, Available: r.Available, Hosting: r.Hosting, VPN: r.VPN, Proxy: r.Proxy, Tor: r.Tor}
}

func (h *ReviewHandler) serveNetwork(w http.ResponseWriter, r *http.Request) {
	purchaseID, ok := parsePurchaseID(r.PathValue("purchase_id"))
	if !ok {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	network, err := h.service.Network(r.Context(), purchaseID)
	if errors.Is(err, models.ErrReviewNotFound) {
		platform.WriteError(w, http.StatusNotFound, "review case not found")
		return
	}
	if err != nil {
		platform.WriteServerError(r.Context(), w, "read review network", err, "purchase_id", purchaseID)
		return
	}
	platform.WriteJSON(w, http.StatusOK, newNetworkResponse(network))
}

func newNetworkResponse(network models.CaseNetwork) networkResponse {
	anchors := make([]networkAnchorResponse, 0, len(network.Anchors))
	for _, a := range network.Anchors {
		linked := make([]linkedEntityResponse, 0, len(a.Linked))
		for _, e := range a.Linked {
			linked = append(linked, linkedEntityResponse{Type: e.Type, Value: e.Value, Purchases: e.Purchases, Status: e.Status})
		}
		anchors = append(anchors, networkAnchorResponse{
			Type: a.Type, Value: a.Value, Blocklisted: a.Blocklisted, LinkedTotal: a.LinkedTotal, Linked: linked,
		})
	}
	return networkResponse{WindowDays: network.WindowDays, Anchors: anchors}
}

// claimFilter reads the claimed query parameter. The only value is "mine",
// which needs a signed-in account; without it the queue is not filtered.
func claimFilter(values []string, actor platform.Actor) (*string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) != 1 || values[0] != "mine" {
		return nil, errors.New("claimed must be mine")
	}
	return &actor.ID, nil
}

func (h *ReviewHandler) serveClaim(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	id := r.PathValue("purchase_id")
	purchaseID, ok := parsePurchaseID(id)
	if !ok {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	claim, err := h.service.Claim(r.Context(), purchaseID, actor.ID)
	if err != nil {
		writeReviewError(r.Context(), w, purchaseID, err, "claim review")
		return
	}
	status := http.StatusCreated
	if claim.Status == models.ClaimAlreadyYours {
		status = http.StatusOK
	}
	platform.WriteJSON(w, status, claimResponse{PurchaseID: id, ClaimedBy: claim.ClaimedBy, ClaimedAt: claim.ClaimedAt.UTC()})
}

func (h *ReviewHandler) serveRelease(w http.ResponseWriter, r *http.Request, _ string) {
	purchaseID, ok := parsePurchaseID(r.PathValue("purchase_id"))
	if !ok {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	if err := h.service.ReleaseClaim(r.Context(), purchaseID); err != nil {
		writeReviewError(r.Context(), w, purchaseID, err, "release claim")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ReviewHandler) serveAssignments(w http.ResponseWriter, r *http.Request, _ string) {
	work, err := h.service.Assignments(r.Context())
	if err != nil {
		platform.WriteServerError(r.Context(), w, "read assignments", err)
		return
	}
	reviewers := make([]reviewerWorkResponse, 0, len(work))
	for _, reviewer := range work {
		reviewers = append(reviewers, newReviewerWorkResponse(reviewer))
	}
	platform.WriteJSON(w, http.StatusOK, assignmentsResponse{Reviewers: reviewers})
}

func newReviewerWorkResponse(work models.ReviewerWork) reviewerWorkResponse {
	claims := make([]openClaimResponse, 0, len(work.OpenClaims))
	for _, c := range work.OpenClaims {
		claims = append(claims, openClaimResponse{
			PurchaseID:  strconv.FormatInt(c.PurchaseID, 10),
			AmountMinor: strconv.FormatInt(c.AmountMinor, 10),
			RuleScore:   c.RuleScore,
			ModelScore:  c.ModelScore,
			ClaimedAt:   c.ClaimedAt.UTC(),
		})
	}
	return reviewerWorkResponse{
		Username: work.Username, Role: work.Role, Disabled: work.Disabled, Decided: work.Decided, OpenClaims: claims,
	}
}

// parsePurchaseID accepts only the canonical decimal form of a positive ID.
func parsePurchaseID(id string) (int64, bool) {
	purchaseID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || purchaseID <= 0 || strconv.FormatInt(purchaseID, 10) != id {
		return 0, false
	}
	return purchaseID, true
}

func (h *ReviewHandler) serveDecision(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	id := r.PathValue("purchase_id")
	purchaseID, ok := parsePurchaseID(id)
	if !ok {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	body, ok := readDecision(w, r)
	if !ok {
		return
	}

	result, err := h.service.Decide(r.Context(), purchaseID, body.Decision, actor.ID, body.blocks())
	if err != nil {
		writeReviewError(r.Context(), w, purchaseID, err, "save review decision")
		return
	}
	platform.WriteJSON(w, http.StatusCreated, decisionResponse{
		PurchaseID:    id,
		Decision:      body.Decision,
		AnalystID:     actor.ID,
		ResolvedAt:    result.ResolvedAt.UTC(),
		DeviceBlocked: result.Blocked.Device,
		CardBlocked:   result.Blocked.Card,
		PlayerBlocked: result.Blocked.Player,
	})
}

// writeReviewError maps the review service errors to status codes. Any other
// error is logged with the action that failed and answered with 500.
func writeReviewError(ctx context.Context, w http.ResponseWriter, purchaseID int64, err error, action string) {
	switch {
	case errors.Is(err, review.ErrInvalidDecision), errors.Is(err, review.ErrBlockNeedsReject),
		errors.Is(err, review.ErrInvalidNetworkAnchor), errors.Is(err, review.ErrInvalidLinkedValue):
		platform.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, review.ErrPurchaseNotFound), errors.Is(err, review.ErrNoOpenClaim):
		platform.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, review.ErrNotUnderReview), errors.Is(err, review.ErrAlreadyDecided),
		errors.Is(err, review.ErrNotClaimed), errors.Is(err, review.ErrClaimedByOther):
		platform.WriteError(w, http.StatusConflict, err.Error())
	default:
		platform.WriteServerError(ctx, w, action, err, "purchase_id", purchaseID)
	}
}

// readDecision reads the decision body. On failure it writes the error
// response and returns false.
func readDecision(w http.ResponseWriter, r *http.Request) (decisionRequest, bool) {
	var body decisionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDecisionBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(&body)
	if err == nil {
		err = dec.Decode(new(any))
		if err == io.EOF {
			err = nil
		} else if err == nil {
			err = errors.New("multiple JSON values")
		}
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		platform.WriteError(w, http.StatusRequestEntityTooLarge, "review decision body is too large")
	case err != nil:
		platform.WriteError(w, http.StatusBadRequest, "request body must be a single review decision object")
	default:
		return body, true
	}
	return decisionRequest{}, false
}

func newReviewResponse(row models.QueueItem) reviewResponse {
	firedRules := row.FiredRules
	if firedRules == nil {
		firedRules = []scoring.FiredRule{}
	}
	return reviewResponse{
		PurchaseID:    strconv.FormatInt(row.PurchaseID, 10),
		TransactionID: row.TransactionID,
		EventTime:     row.EventTime.UTC(),
		QueuedAt:      row.DecidedAt.UTC(),
		AmountMinor:   strconv.FormatInt(row.AmountMinor, 10),
		PlayerID:      row.PlayerID,
		DeviceID:      row.DeviceID,
		RuleScore:     row.RuleScore,
		ModelScore:    row.ModelScore,
		ModelVersion:  row.ModelVersion,
		FiredRules:    firedRules,
		ClaimedBy:     row.ClaimedBy,
		ClaimedAt:     row.ClaimedAt,
	}
}

func encodeCursor(order models.QueueOrder, cursor models.QueueCursor) string {
	value := strings.Join([]string{
		string(order), cursor.DecidedAt.UTC().Format(time.RFC3339Nano),
		strconv.FormatInt(cursor.PurchaseID, 10), strconv.FormatFloat(cursor.ScoreKey, 'g', -1, 64),
	}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func parseCursor(values []string, order models.QueueOrder) (*models.QueueCursor, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) != 1 || len(values[0]) > 128 {
		return nil, errors.New("expected one cursor of at most 128 characters")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(data), "|")
	if len(parts) != 4 || models.QueueOrder(parts[0]) != order {
		return nil, errors.New("cursor does not match sort order")
	}
	decidedAt, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return nil, err
	}
	purchaseID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || purchaseID <= 0 {
		return nil, errors.New("invalid cursor purchase ID")
	}
	scoreKey, err := strconv.ParseFloat(parts[3], 64)
	validScore := scoreKey >= -math.MaxInt32 && scoreKey <= 0 && scoreKey == math.Trunc(scoreKey)
	if order == models.QueueByModelScore {
		validScore = scoreKey >= -1 && (scoreKey <= 0 || scoreKey == 1)
	}
	if err != nil || !validScore {
		return nil, errors.New("invalid cursor score")
	}
	return &models.QueueCursor{DecidedAt: decidedAt.UTC(), PurchaseID: purchaseID, ScoreKey: scoreKey}, nil
}
