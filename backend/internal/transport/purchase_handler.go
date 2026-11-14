package transport

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

const maxSearchLength = 200

type purchaseMatchResponse struct {
	PurchaseID    string    `json:"purchase_id"`
	TransactionID string    `json:"transaction_id"`
	PlayerID      string    `json:"player_id"`
	AmountMinor   string    `json:"amount_minor"`
	EventTime     time.Time `json:"event_time"`
	Outcome       string    `json:"outcome"`
	Review        *string   `json:"review"`
	MatchedBy     string    `json:"matched_by"`
}

type purchaseSearchResponse struct {
	Items []purchaseMatchResponse `json:"items"`
}

type purchaseCaseResponse struct {
	caseResponse
	Outcome    string                  `json:"outcome"`
	Resolution *caseResolutionResponse `json:"resolution"`
}

func (h *ReviewHandler) serveSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || utf8.RuneCountInString(query) > maxSearchLength {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{
			Error:  "invalid request",
			Fields: map[string]string{"q": "must be 1 to 200 characters"},
		})
		return
	}
	matches, err := h.service.SearchPurchases(r.Context(), query)
	if err != nil {
		platform.WriteServerError(r.Context(), w, "search purchases", err)
		return
	}
	items := make([]purchaseMatchResponse, 0, len(matches))
	for _, m := range matches {
		var review *string
		if m.Review != "" {
			review = &m.Review
		}
		items = append(items, purchaseMatchResponse{
			PurchaseID:    strconv.FormatInt(m.PurchaseID, 10),
			TransactionID: m.TransactionID,
			PlayerID:      m.PlayerID,
			AmountMinor:   strconv.FormatInt(m.AmountMinor, 10),
			EventTime:     m.EventTime.UTC(),
			Outcome:       m.Outcome,
			Review:        review,
			MatchedBy:     m.MatchedBy,
		})
	}
	platform.WriteJSON(w, http.StatusOK, purchaseSearchResponse{Items: items})
}

func (h *ReviewHandler) servePurchase(w http.ResponseWriter, r *http.Request) {
	purchaseID, err := strconv.ParseInt(r.PathValue("purchase_id"), 10, 64)
	if err != nil || purchaseID <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	item, err := h.service.PurchaseCase(r.Context(), purchaseID)
	if errors.Is(err, models.ErrPurchaseNotFound) {
		platform.WriteError(w, http.StatusNotFound, "purchase not found")
		return
	}
	if err != nil {
		platform.WriteServerError(r.Context(), w, "read purchase", err, "purchase_id", purchaseID)
		return
	}
	response := purchaseCaseResponse{caseResponse: newCaseResponse(item.CaseItem), Outcome: item.Outcome}
	response.ModelReasons = h.modelReasons(r.Context(), item.CaseItem)
	if item.Resolution != nil {
		response.Resolution = &caseResolutionResponse{
			Decision: item.Resolution.Decision, AnalystID: item.Resolution.AnalystID, ResolvedAt: item.Resolution.ResolvedAt.UTC(),
		}
	}
	platform.WriteJSON(w, http.StatusOK, response)
}
