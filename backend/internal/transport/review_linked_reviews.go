package transport

import (
	"errors"
	"net/http"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

type linkedReviewsResponse struct {
	WindowDays int              `json:"window_days"`
	Total      int64            `json:"total"`
	Items      []reviewResponse `json:"items"`
}

func (h *ReviewHandler) serveLinkedReviews(w http.ResponseWriter, r *http.Request) {
	purchaseID, ok := parsePurchaseID(r.PathValue("purchase_id"))
	if !ok {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	linked, err := h.service.LinkedReviews(r.Context(), purchaseID)
	if errors.Is(err, models.ErrReviewNotFound) {
		platform.WriteError(w, http.StatusNotFound, "review case not found")
		return
	}
	if err != nil {
		platform.WriteServerError(r.Context(), w, "read linked reviews", err, "purchase_id", purchaseID)
		return
	}
	items := make([]reviewResponse, 0, len(linked.Items))
	for _, item := range linked.Items {
		items = append(items, newReviewResponse(item))
	}
	platform.WriteJSON(w, http.StatusOK, linkedReviewsResponse{WindowDays: linked.WindowDays, Total: linked.Total, Items: items})
}
