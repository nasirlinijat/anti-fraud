package transport

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

type blockedPurchaseResponse struct {
	PurchaseID     string              `json:"purchase_id"`
	TransactionID  string              `json:"transaction_id"`
	PlayerID       string              `json:"player_id"`
	AmountMinor    string              `json:"amount_minor"`
	EventTime      time.Time           `json:"event_time"`
	CardCountry    *string             `json:"card_country"`
	IPCountry      *string             `json:"ip_country"`
	DeviceID       *string             `json:"device_id"`
	Reason         string              `json:"reason"`
	RuleScore      int                 `json:"rule_score"`
	BlockThreshold int                 `json:"block_threshold"`
	RulesetVersion int                 `json:"ruleset_version"`
	FiredRules     []scoring.FiredRule `json:"fired_rules"`
	DecidedAt      time.Time           `json:"decided_at"`
}

type blockedPurchasesResponse struct {
	Items []blockedPurchaseResponse `json:"items"`
	Next  string                    `json:"next_cursor,omitempty"`
}

// blocked lists the run's blocked purchases, newest first. before is the
// next_cursor of the previous page.
func (h *SimulationHandler) blocked(w http.ResponseWriter, r *http.Request) {
	limit, ok := readPageLimit(w, r)
	if !ok {
		return
	}
	before, ok := readBlockedCursor(w, r)
	if !ok {
		return
	}
	items, next, err := h.service.BlockedPurchases(r.Context(), r.PathValue("id"), before, limit)
	if errors.Is(err, models.ErrSimulationNotFound) {
		platform.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		simulationFailure(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, newBlockedPurchasesResponse(items, next))
}

// readBlockedCursor reads the optional purchase ID cursor, 0 when absent. On
// failure it writes the error response and returns false.
func readBlockedCursor(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("before")
	if raw == "" {
		return 0, true
	}
	before, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || before <= 0 {
		platform.WriteError(w, http.StatusBadRequest, models.ErrInvalidSimulationCursor.Error())
		return 0, false
	}
	return before, true
}

func newBlockedPurchasesResponse(items []models.BlockedPurchase, next int64) blockedPurchasesResponse {
	response := blockedPurchasesResponse{Items: make([]blockedPurchaseResponse, 0, len(items))}
	if next > 0 {
		response.Next = strconv.FormatInt(next, 10)
	}
	for _, b := range items {
		response.Items = append(response.Items, blockedPurchaseResponse{
			PurchaseID:     strconv.FormatInt(b.PurchaseID, 10),
			TransactionID:  b.TransactionID,
			PlayerID:       b.PlayerID,
			AmountMinor:    strconv.FormatInt(b.AmountMinor, 10),
			EventTime:      b.EventTime,
			CardCountry:    b.CardCountry,
			IPCountry:      b.IPCountry,
			DeviceID:       b.DeviceID,
			Reason:         b.Reason,
			RuleScore:      b.RuleScore,
			BlockThreshold: b.BlockThreshold,
			RulesetVersion: b.RulesetVersion,
			FiredRules:     b.FiredRules,
			DecidedAt:      b.DecidedAt,
		})
	}
	return response
}
