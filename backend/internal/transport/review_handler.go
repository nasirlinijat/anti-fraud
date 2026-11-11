package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
)

const maxDecisionBytes = 1024

type ReviewService interface {
	Queue(ctx context.Context, order models.QueueOrder, after *models.QueueCursor) (review.Page, error)
	Case(ctx context.Context, purchaseID int64) (models.CaseItem, error)
	Decide(ctx context.Context, purchaseID int64, decision, analystID string, blockDevice bool) (models.Resolution, error)
}

type ReviewHandler struct {
	service      ReviewService
	modelEnabled bool
}

func NewReviewHandler(service ReviewService, modelEnabled bool) *ReviewHandler {
	return &ReviewHandler{service: service, modelEnabled: modelEnabled}
}

// RegisterReviewRoutes exposes the queue, case evidence, and decisions to authenticated analysts.
func RegisterReviewRoutes(mux *http.ServeMux, h *ReviewHandler, keys platform.AnalystKeys) {
	mux.HandleFunc("GET /reviews", requireAnalyst(keys, func(w http.ResponseWriter, r *http.Request, _ string) {
		h.serveQueue(w, r)
	}))
	mux.HandleFunc("GET /reviews/{purchase_id}", requireAnalyst(keys, func(w http.ResponseWriter, r *http.Request, _ string) {
		h.serveCase(w, r)
	}))
	mux.HandleFunc("POST /reviews/{purchase_id}/decision", requireAnalyst(keys, h.serveDecision))
}

func requireAnalyst(keys platform.AnalystKeys, next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !keys.Configured() {
			platform.WriteError(w, http.StatusServiceUnavailable, "review access is not configured")
			return
		}
		headers := r.Header.Values(platform.APIKeyHeader)
		if len(headers) == 1 {
			if analystID := keys.Analyst(headers[0]); analystID != "" {
				next(w, r, analystID)
				return
			}
		}
		platform.WriteError(w, http.StatusUnauthorized, "missing or invalid analyst key")
	}
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

type caseResponse struct {
	reviewResponse
	RulesetVersion  int                      `json:"ruleset_version"`
	PlayerPurchases []playerPurchaseResponse `json:"player_purchases"`
	DeviceActivity  *deviceActivityResponse  `json:"device_activity"`
}

type queueResponse struct {
	Items        []reviewResponse `json:"items"`
	NextCursor   *string          `json:"next_cursor"`
	ModelEnabled bool             `json:"model_enabled"`
}

type decisionResponse struct {
	PurchaseID string    `json:"purchase_id"`
	Decision   string    `json:"decision"`
	AnalystID  string    `json:"analyst_id"`
	ResolvedAt time.Time `json:"resolved_at"`
	// DeviceBlocked is true when this rejection added the device to the blocklist.
	DeviceBlocked bool `json:"device_blocked"`
}

// decisionRequest is the body of a review decision.
type decisionRequest struct {
	Decision    string `json:"decision"`
	BlockDevice bool   `json:"block_device"`
}

func (h *ReviewHandler) serveQueue(w http.ResponseWriter, r *http.Request) {
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
	page, err := h.service.Queue(r.Context(), order, after)
	if err != nil {
		log.Printf("read review queue: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var nextCursor *string
	if page.Next != nil {
		token := encodeCursor(order, *page.Next)
		nextCursor = &token
	}
	items := make([]reviewResponse, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, newReviewResponse(row))
	}
	platform.WriteJSON(w, http.StatusOK, queueResponse{Items: items, NextCursor: nextCursor, ModelEnabled: h.modelEnabled})
}

func (h *ReviewHandler) serveCase(w http.ResponseWriter, r *http.Request) {
	purchaseID, err := strconv.ParseInt(r.PathValue("purchase_id"), 10, 64)
	if err != nil || purchaseID <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	item, err := h.service.Case(r.Context(), purchaseID)
	if errors.Is(err, models.ErrReviewNotFound) {
		platform.WriteError(w, http.StatusNotFound, "review case not found")
		return
	}
	if err != nil {
		log.Printf("read review case: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
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
	platform.WriteJSON(w, http.StatusOK, caseResponse{
		reviewResponse:  newReviewResponse(item.QueueItem),
		RulesetVersion:  item.RulesetVersion,
		PlayerPurchases: playerPurchases,
		DeviceActivity:  deviceActivity,
	})
}

func (h *ReviewHandler) serveDecision(w http.ResponseWriter, r *http.Request, analystID string) {
	id := r.PathValue("purchase_id")
	purchaseID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || purchaseID <= 0 || strconv.FormatInt(purchaseID, 10) != id {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	body, ok := readDecision(w, r)
	if !ok {
		return
	}

	result, err := h.service.Decide(r.Context(), purchaseID, body.Decision, analystID, body.BlockDevice)
	switch {
	case errors.Is(err, review.ErrInvalidDecision), errors.Is(err, review.ErrBlockDeviceNeedsReject):
		platform.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, review.ErrPurchaseNotFound):
		platform.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, review.ErrNotUnderReview), errors.Is(err, review.ErrAlreadyDecided):
		platform.WriteError(w, http.StatusConflict, err.Error())
	case err != nil:
		log.Printf("save review decision: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
	default:
		platform.WriteJSON(w, http.StatusCreated, decisionResponse{
			PurchaseID:    id,
			Decision:      body.Decision,
			AnalystID:     analystID,
			ResolvedAt:    result.ResolvedAt.UTC(),
			DeviceBlocked: result.DeviceBlocked,
		})
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
