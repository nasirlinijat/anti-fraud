package reviews

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const pageSize = 100

type queueReader interface {
	queue(context.Context, int, queueOrder, *queueCursor) ([]QueueItem, error)
}

type caseReader interface {
	Case(context.Context, int64) (CaseItem, error)
}

type reviewStore interface {
	queueReader
	caseReader
	resolve(ctx context.Context, purchaseID int64, decision, analystID string, blockDevice bool) (resolution, error)
}

// RegisterRoutes exposes the queue, case evidence, and decisions to authenticated analysts.
func RegisterRoutes(mux *http.ServeMux, store reviewStore, keys platform.AnalystKeys, modelEnabled bool) {
	mux.HandleFunc("GET /reviews", requireAnalyst(keys, func(w http.ResponseWriter, r *http.Request, _ string) {
		serveQueue(w, r, store, modelEnabled)
	}))
	mux.HandleFunc("GET /reviews/{purchase_id}", requireAnalyst(keys, func(w http.ResponseWriter, r *http.Request, _ string) {
		serveCase(w, r, store)
	}))
	mux.HandleFunc("POST /reviews/{purchase_id}/decision", requireAnalyst(keys, func(w http.ResponseWriter, r *http.Request, analystID string) {
		serveResolution(w, r, store, analystID)
	}))
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

func serveQueue(w http.ResponseWriter, r *http.Request, store queueReader, modelEnabled bool) {
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid query parameters")
		return
	}
	order := byRuleScore
	if values, present := params["sort"]; present {
		if len(values) != 1 || (queueOrder(values[0]) != byRuleScore && queueOrder(values[0]) != byModelScore) {
			platform.WriteError(w, http.StatusBadRequest, "invalid sort order")
			return
		}
		order = queueOrder(values[0])
	}
	after, err := parseCursor(params["cursor"], order)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	rows, err := store.queue(r.Context(), pageSize+1, order, after)
	if err != nil {
		log.Printf("read review queue: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var nextCursor *string
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		token := encodeCursor(order, cursorFor(order, rows[pageSize-1]))
		nextCursor = &token
	}
	items := make([]reviewResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, newReviewResponse(row))
	}
	platform.WriteJSON(w, http.StatusOK, queueResponse{Items: items, NextCursor: nextCursor, ModelEnabled: modelEnabled})
}

func serveCase(w http.ResponseWriter, r *http.Request, store caseReader) {
	purchaseID, err := strconv.ParseInt(r.PathValue("purchase_id"), 10, 64)
	if err != nil || purchaseID <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	item, err := store.Case(r.Context(), purchaseID)
	if errors.Is(err, ErrNotFound) {
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

func newReviewResponse(row QueueItem) reviewResponse {
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

func encodeCursor(order queueOrder, cursor queueCursor) string {
	value := strings.Join([]string{
		string(order), cursor.decidedAt.UTC().Format(time.RFC3339Nano),
		strconv.FormatInt(cursor.purchaseID, 10), strconv.FormatFloat(cursor.scoreKey, 'g', -1, 64),
	}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func parseCursor(values []string, order queueOrder) (*queueCursor, error) {
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
	if len(parts) != 4 || queueOrder(parts[0]) != order {
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
	if order == byModelScore {
		validScore = scoreKey >= -1 && (scoreKey <= 0 || scoreKey == 1)
	}
	if err != nil || !validScore {
		return nil, errors.New("invalid cursor score")
	}
	return &queueCursor{decidedAt: decidedAt.UTC(), purchaseID: purchaseID, scoreKey: scoreKey}, nil
}
