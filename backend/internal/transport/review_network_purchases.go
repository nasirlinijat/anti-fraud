package transport

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

type linkedPurchasesResponse struct {
	Anchor     models.BlocklistType     `json:"anchor"`
	Value      string                   `json:"value"`
	WindowDays int                      `json:"window_days"`
	Total      int64                    `json:"total"`
	Purchases  []linkedPurchaseResponse `json:"purchases"`
}

type linkedPurchaseResponse struct {
	PurchaseID     string          `json:"purchase_id"`
	TransactionID  string          `json:"transaction_id"`
	EventTime      time.Time       `json:"event_time"`
	AmountMinor    string          `json:"amount_minor"`
	Outcome        scoring.Outcome `json:"outcome"`
	ReviewDecision *string         `json:"review_decision"`
	ClaimedBy      *string         `json:"claimed_by"`
	AwaitingReview bool            `json:"awaiting_review"`
}

func (h *ReviewHandler) serveNetworkPurchases(w http.ResponseWriter, r *http.Request) {
	purchaseID, ok := parsePurchaseID(r.PathValue("purchase_id"))
	if !ok {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	anchor, value, problem := linkedValueQuery(r.URL.RawQuery)
	if problem != "" {
		platform.WriteError(w, http.StatusBadRequest, problem)
		return
	}
	linked, err := h.service.NetworkPurchases(r.Context(), purchaseID, models.BlocklistType(anchor), value)
	if errors.Is(err, models.ErrReviewNotFound) {
		platform.WriteError(w, http.StatusNotFound, "review case not found")
		return
	}
	if err != nil {
		writeReviewError(r.Context(), w, purchaseID, err, "read linked purchases")
		return
	}
	platform.WriteJSON(w, http.StatusOK, newLinkedPurchasesResponse(linked))
}

// linkedValueQuery reads the anchor and value query parameters, each of which
// must appear once. The service checks what they contain. A problem is
// returned as the error message for the client.
func linkedValueQuery(rawQuery string) (anchor, value, problem string) {
	params, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", "", "invalid query parameters"
	}
	if len(params["anchor"]) != 1 {
		return "", "", "anchor must be given once"
	}
	if len(params["value"]) != 1 {
		return "", "", "value must be given once"
	}
	return params["anchor"][0], params["value"][0], ""
}

func newLinkedPurchasesResponse(linked models.LinkedPurchases) linkedPurchasesResponse {
	purchases := make([]linkedPurchaseResponse, 0, len(linked.Purchases))
	for _, p := range linked.Purchases {
		purchases = append(purchases, linkedPurchaseResponse{
			PurchaseID:     strconv.FormatInt(p.PurchaseID, 10),
			TransactionID:  p.TransactionID,
			EventTime:      p.EventTime.UTC(),
			AmountMinor:    strconv.FormatInt(p.AmountMinor, 10),
			Outcome:        p.Outcome,
			ReviewDecision: p.ReviewDecision,
			ClaimedBy:      p.ClaimedBy,
			AwaitingReview: p.AwaitingReview,
		})
	}
	return linkedPurchasesResponse{
		Anchor: linked.Anchor, Value: linked.Value, WindowDays: linked.WindowDays, Total: linked.Total, Purchases: purchases,
	}
}
