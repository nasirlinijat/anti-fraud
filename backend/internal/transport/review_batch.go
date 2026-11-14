package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
)

// maxBatchBytes fits 50 purchase IDs of the longest form with room to spare.
const maxBatchBytes = 4096

const claimNeedsSessionMessage = "claiming a purchase needs a signed-in account"

type batchClaimRequest struct {
	PurchaseIDs []string `json:"purchase_ids"`
}

// batchDecisionRequest is one decision for several purchases. The block
// fields apply to each purchase's own device, card and player.
type batchDecisionRequest struct {
	Decision    string   `json:"decision"`
	PurchaseIDs []string `json:"purchase_ids"`
	BlockDevice bool     `json:"block_device"`
	BlockCard   bool     `json:"block_card"`
	BlockPlayer bool     `json:"block_player"`
}

type batchClaimResponse struct {
	Results []batchClaimResult `json:"results"`
}

// batchClaimResult names the holder for claimed, already_yours and
// held_by_other, and has null claim fields otherwise.
type batchClaimResult struct {
	PurchaseID string             `json:"purchase_id"`
	Status     review.BatchStatus `json:"status"`
	ClaimedBy  *string            `json:"claimed_by"`
	ClaimedAt  *time.Time         `json:"claimed_at"`
}

type batchDecisionResponse struct {
	Decision  string                `json:"decision"`
	AnalystID string                `json:"analyst_id"`
	Results   []batchDecisionResult `json:"results"`
}

// batchDecisionResult has a resolved_at only for saved. The blocked fields
// are true when this rejection added that value of the purchase.
type batchDecisionResult struct {
	PurchaseID    string             `json:"purchase_id"`
	Status        review.BatchStatus `json:"status"`
	ResolvedAt    *time.Time         `json:"resolved_at"`
	DeviceBlocked bool               `json:"device_blocked"`
	CardBlocked   bool               `json:"card_blocked"`
	PlayerBlocked bool               `json:"player_blocked"`
}

// serveBatchClaim answers 200 with a result per purchase, even when some
// could not be claimed.
func (h *ReviewHandler) serveBatchClaim(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var body batchClaimRequest
	if !readBatchBody(w, r, &body, "batch claim") {
		return
	}
	purchaseIDs, ok := parsePurchaseIDs(body.PurchaseIDs)
	if !ok {
		platform.WriteError(w, http.StatusBadRequest, "purchase_ids must hold positive decimal purchase IDs")
		return
	}
	results, err := h.service.ClaimMany(r.Context(), purchaseIDs, actor.ID)
	if err != nil {
		writeBatchError(r.Context(), w, err, "claim reviews")
		return
	}
	logClaimErrors(r.Context(), results)
	platform.WriteJSON(w, http.StatusOK, newBatchClaimResponse(results))
}

// serveBatchDecision decides each purchase on its own, like serveDecision: a
// session can decide only what it has claimed.
func (h *ReviewHandler) serveBatchDecision(w http.ResponseWriter, r *http.Request, actor platform.Actor) {
	var body batchDecisionRequest
	if !readBatchBody(w, r, &body, "batch decision") {
		return
	}
	purchaseIDs, ok := parsePurchaseIDs(body.PurchaseIDs)
	if !ok {
		platform.WriteError(w, http.StatusBadRequest, "purchase_ids must hold positive decimal purchase IDs")
		return
	}
	results, err := h.decideMany(r.Context(), body, purchaseIDs, actor)
	if err != nil {
		writeBatchError(r.Context(), w, err, "save review decisions")
		return
	}
	logDecisionErrors(r.Context(), results)
	platform.WriteJSON(w, http.StatusOK, newBatchDecisionResponse(body.Decision, actor.ID, results))
}

func (h *ReviewHandler) decideMany(ctx context.Context, body batchDecisionRequest, purchaseIDs []int64, actor platform.Actor) ([]review.DecisionResult, error) {
	d := review.BatchDecision{
		PurchaseIDs: purchaseIDs,
		Decision:    body.Decision,
		AnalystID:   actor.ID,
		Blocks:      decisionRequest{BlockDevice: body.BlockDevice, BlockCard: body.BlockCard, BlockPlayer: body.BlockPlayer}.blocks(),
	}
	return h.service.DecideMany(ctx, d)
}

// parsePurchaseIDs accepts only the canonical form of each ID, as
// parsePurchaseID does. The service checks the length of the list.
func parsePurchaseIDs(ids []string) ([]int64, bool) {
	purchaseIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		purchaseID, ok := parsePurchaseID(id)
		if !ok {
			return nil, false
		}
		purchaseIDs = append(purchaseIDs, purchaseID)
	}
	return purchaseIDs, true
}

// readBatchBody decodes a single JSON object into dst. On failure it writes
// the error response and returns false.
func readBatchBody(w http.ResponseWriter, r *http.Request, dst any, what string) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBatchBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
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
		platform.WriteError(w, http.StatusRequestEntityTooLarge, what+" body is too large")
	case err != nil:
		platform.WriteError(w, http.StatusBadRequest, "request body must be a single "+what+" object")
	default:
		return true
	}
	return false
}

// writeBatchError answers 400 for a request the service refused before
// trying any purchase.
func writeBatchError(ctx context.Context, w http.ResponseWriter, err error, action string) {
	if errors.Is(err, review.ErrInvalidBatchSize) || errors.Is(err, review.ErrDuplicatePurchaseID) ||
		errors.Is(err, review.ErrInvalidDecision) || errors.Is(err, review.ErrBlockNeedsReject) {
		platform.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	platform.WriteServerError(ctx, w, action, err)
}

func logClaimErrors(ctx context.Context, results []review.ClaimResult) {
	for _, result := range results {
		if result.Status == review.BatchError {
			platform.LogServerError(ctx, "claim review", result.Err, "purchase_id", result.PurchaseID)
		}
	}
}

func logDecisionErrors(ctx context.Context, results []review.DecisionResult) {
	for _, result := range results {
		if result.Status == review.BatchError {
			platform.LogServerError(ctx, "save review decision", result.Err, "purchase_id", result.PurchaseID)
		}
	}
}

func newBatchClaimResponse(results []review.ClaimResult) batchClaimResponse {
	resp := batchClaimResponse{Results: make([]batchClaimResult, 0, len(results))}
	for _, result := range results {
		item := batchClaimResult{PurchaseID: strconv.FormatInt(result.PurchaseID, 10), Status: result.Status}
		if holdsClaim(result.Status) {
			claimedBy, claimedAt := result.Claim.ClaimedBy, result.Claim.ClaimedAt.UTC()
			item.ClaimedBy, item.ClaimedAt = &claimedBy, &claimedAt
		}
		resp.Results = append(resp.Results, item)
	}
	return resp
}

func holdsClaim(status review.BatchStatus) bool {
	return status == review.BatchClaimed || status == review.BatchAlreadyYours || status == review.BatchHeldByOther
}

func newBatchDecisionResponse(decision, analystID string, results []review.DecisionResult) batchDecisionResponse {
	resp := batchDecisionResponse{Decision: decision, AnalystID: analystID, Results: make([]batchDecisionResult, 0, len(results))}
	for _, result := range results {
		item := batchDecisionResult{PurchaseID: strconv.FormatInt(result.PurchaseID, 10), Status: result.Status}
		if result.Status == review.BatchSaved {
			resolvedAt := result.Resolution.ResolvedAt.UTC()
			item.ResolvedAt = &resolvedAt
			blocked := result.Resolution.Blocked
			item.DeviceBlocked, item.CardBlocked, item.PlayerBlocked = blocked.Device, blocked.Card, blocked.Player
		}
		resp.Results = append(resp.Results, item)
	}
	return resp
}
