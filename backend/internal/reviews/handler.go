package reviews

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
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
	queue(context.Context, int, *queueCursor) ([]QueueItem, error)
}

// RegisterRoutes exposes the read-only review queue behind API key authentication.
func RegisterRoutes(mux *http.ServeMux, store queueReader, apiKey string) {
	authorized := platform.RequireAPIKey(apiKey, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveQueue(w, r, store)
	}))
	mux.HandleFunc("GET /reviews", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		authorized.ServeHTTP(w, r)
	})
}

type reviewResponse struct {
	PurchaseID    string              `json:"purchase_id"`
	TransactionID string              `json:"transaction_id"`
	EventTime     time.Time           `json:"event_time"`
	QueuedAt      time.Time           `json:"queued_at"`
	AmountMinor   string              `json:"amount_minor"`
	PlayerID      string              `json:"player_id"`
	RuleScore     int                 `json:"rule_score"`
	ModelScore    *float64            `json:"model_score"`
	FiredRules    []scoring.FiredRule `json:"fired_rules"`
}

type queueResponse struct {
	Items      []reviewResponse `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

func serveQueue(w http.ResponseWriter, r *http.Request, store queueReader) {
	after, err := parseCursor(r.URL.RawQuery)
	if err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	rows, err := store.queue(r.Context(), pageSize+1, after)
	if err != nil {
		log.Printf("read review queue: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var nextCursor *string
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		last := rows[pageSize-1]
		token := encodeCursor(queueCursor{decidedAt: last.DecidedAt, purchaseID: last.PurchaseID})
		nextCursor = &token
	}
	items := make([]reviewResponse, 0, len(rows))
	for _, row := range rows {
		firedRules := row.FiredRules
		if firedRules == nil {
			firedRules = []scoring.FiredRule{}
		}
		items = append(items, reviewResponse{
			PurchaseID:    strconv.FormatInt(row.PurchaseID, 10),
			TransactionID: row.TransactionID,
			EventTime:     row.EventTime.UTC(),
			QueuedAt:      row.DecidedAt.UTC(),
			AmountMinor:   strconv.FormatInt(row.AmountMinor, 10),
			PlayerID:      row.PlayerID,
			RuleScore:     row.RuleScore,
			ModelScore:    row.ModelScore,
			FiredRules:    firedRules,
		})
	}
	platform.WriteJSON(w, http.StatusOK, queueResponse{Items: items, NextCursor: nextCursor})
}

func encodeCursor(cursor queueCursor) string {
	value := cursor.decidedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(cursor.purchaseID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func parseCursor(rawQuery string) (*queueCursor, error) {
	params, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	values, present := params["cursor"]
	if !present {
		return nil, nil
	}
	if len(values) != 1 || len(values[0]) > 128 {
		return nil, errors.New("expected one cursor of at most 128 characters")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
	if err != nil {
		return nil, err
	}
	timestamp, id, ok := strings.Cut(string(data), "|")
	if !ok {
		return nil, errors.New("missing cursor separator")
	}
	decidedAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return nil, err
	}
	purchaseID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || purchaseID <= 0 {
		return nil, errors.New("invalid cursor purchase ID")
	}
	return &queueCursor{decidedAt: decidedAt.UTC(), purchaseID: purchaseID}, nil
}
