package transport

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
)

type previewChangeResponse struct {
	PurchaseID      string    `json:"purchase_id"`
	TransactionID   string    `json:"transaction_id"`
	DecidedAt       time.Time `json:"decided_at"`
	Before          string    `json:"before"`
	After           string    `json:"after"`
	RuleScoreBefore int       `json:"rule_score_before"`
	RuleScoreAfter  int       `json:"rule_score_after"`
	Uncertain       bool      `json:"uncertain"`
}

type rulesetPreviewResponse struct {
	Decisions          int                     `json:"decisions"`
	Before             models.PreviewCounts    `json:"before"`
	After              models.PreviewCounts    `json:"after"`
	Changed            int                     `json:"changed"`
	Uncertain          int                     `json:"uncertain"`
	UncertainRules     []string                `json:"uncertain_rules"`
	MissingModelScores int                     `json:"missing_model_scores"`
	Changes            []previewChangeResponse `json:"changes"`
}

func (h *RulesetHandler) preview(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeConfigPayload(w, r)
	if !ok {
		return
	}
	result, err := h.service.Preview(r.Context(), payload.draft())
	var invalid *ruleset.ValidationError
	if errors.As(err, &invalid) {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: invalid.Fields})
		return
	}
	if err != nil {
		platform.WriteServerError(r.Context(), w, "preview ruleset", err)
		return
	}
	changes := make([]previewChangeResponse, 0, len(result.Changes))
	for _, c := range result.Changes {
		changes = append(changes, previewChangeResponse{
			PurchaseID: strconv.FormatInt(c.PurchaseID, 10), TransactionID: c.TransactionID, DecidedAt: c.DecidedAt.UTC(),
			Before: c.Before, After: c.After, RuleScoreBefore: c.RuleScoreBefore, RuleScoreAfter: c.RuleScoreAfter, Uncertain: c.Uncertain,
		})
	}
	uncertainRules := result.UncertainRules
	if uncertainRules == nil {
		uncertainRules = []string{}
	}
	platform.WriteJSON(w, http.StatusOK, rulesetPreviewResponse{
		Decisions: result.Decisions, Before: result.Before, After: result.After, Changed: result.Changed,
		Uncertain: result.Uncertain, UncertainRules: uncertainRules, MissingModelScores: result.MissingModelScores, Changes: changes,
	})
}
