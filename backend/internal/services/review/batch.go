package review

import (
	"context"
	"errors"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

// MaxBatchPurchases is the most purchases one batch claim or decision can name.
const MaxBatchPurchases = 50

// BatchStatus is what happened to one purchase of a batch claim or decision.
type BatchStatus string

// Batch statuses of claims and decisions. A claim reports the first three or
// a shared failure; a decision reports saved, not_claimed, or a shared failure.
const (
	BatchClaimed        BatchStatus = "claimed"
	BatchAlreadyYours   BatchStatus = "already_yours"
	BatchHeldByOther    BatchStatus = "held_by_other"
	BatchSaved          BatchStatus = "saved"
	BatchAlreadyDecided BatchStatus = "already_decided"
	BatchNotClaimed     BatchStatus = "not_claimed"
	BatchNotFound       BatchStatus = "not_found"
	BatchNotReview      BatchStatus = "not_review"
	BatchError          BatchStatus = "error"
)

// ClaimResult is what happened when one purchase of a batch was claimed.
type ClaimResult struct {
	PurchaseID int64
	Status     BatchStatus
	// Claim names the holder for BatchClaimed, BatchAlreadyYours and
	// BatchHeldByOther, and is empty otherwise.
	Claim models.Claim
	// Err is the unexpected error behind BatchError.
	Err error
}

// DecisionResult is what happened when one purchase of a batch was decided.
type DecisionResult struct {
	PurchaseID int64
	Status     BatchStatus
	// Resolution is the saved decision for BatchSaved, and empty otherwise.
	Resolution models.Resolution
	// Err is the unexpected error behind BatchError.
	Err error
}

// BatchDecision is one decision for several purchases. AnalystID is the
// username of the signed-in account.
type BatchDecision struct {
	PurchaseIDs []int64
	Decision    string
	AnalystID   string
	// Blocks apply to each purchase's own device, card and player.
	Blocks models.ReviewBlocks
}

// ClaimMany claims each purchase for the signed-in account with Claim, one
// after another in the given order, and reports what happened to each. It
// returns ErrInvalidBatchSize or ErrDuplicatePurchaseID before claiming
// anything. An unexpected error on one purchase gives it BatchError and the
// rest are still claimed.
func (s *Service) ClaimMany(ctx context.Context, purchaseIDs []int64, username string) ([]ClaimResult, error) {
	if err := checkBatch(purchaseIDs); err != nil {
		return nil, err
	}
	results := make([]ClaimResult, 0, len(purchaseIDs))
	for _, id := range purchaseIDs {
		results = append(results, s.claimOne(ctx, id, username))
	}
	return results, nil
}

func (s *Service) claimOne(ctx context.Context, purchaseID int64, username string) ClaimResult {
	if err := ctx.Err(); err != nil {
		return ClaimResult{PurchaseID: purchaseID, Status: BatchError, Err: err}
	}
	claim, err := s.Claim(ctx, purchaseID, username)
	if err == nil {
		status := BatchClaimed
		if claim.Status == models.ClaimAlreadyYours {
			status = BatchAlreadyYours
		}
		return ClaimResult{PurchaseID: purchaseID, Status: status, Claim: claim}
	}
	result := ClaimResult{PurchaseID: purchaseID, Status: failureStatus(err)}
	switch result.Status {
	case BatchHeldByOther:
		result.Claim = claim
	case BatchError:
		result.Err = err
	}
	return result
}

// DecideMany decides each claimed purchase in its own transaction.
// One purchase's failure does not undo successful decisions on the others.
func (s *Service) DecideMany(ctx context.Context, d BatchDecision) ([]DecisionResult, error) {
	if err := checkBatch(d.PurchaseIDs); err != nil {
		return nil, err
	}
	if err := checkDecision(d.Decision, d.Blocks); err != nil {
		return nil, err
	}
	results := make([]DecisionResult, 0, len(d.PurchaseIDs))
	for _, id := range d.PurchaseIDs {
		results = append(results, s.decideOne(ctx, id, d))
	}
	return results, nil
}

func (s *Service) decideOne(ctx context.Context, purchaseID int64, d BatchDecision) DecisionResult {
	if err := ctx.Err(); err != nil {
		return DecisionResult{PurchaseID: purchaseID, Status: BatchError, Err: err}
	}
	resolution, err := s.Decide(ctx, purchaseID, d.Decision, d.AnalystID, d.Blocks)
	if err == nil {
		return DecisionResult{PurchaseID: purchaseID, Status: BatchSaved, Resolution: resolution}
	}
	result := DecisionResult{PurchaseID: purchaseID, Status: failureStatus(err)}
	if result.Status == BatchError {
		result.Err = err
	}
	return result
}

func checkBatch(purchaseIDs []int64) error {
	if len(purchaseIDs) == 0 || len(purchaseIDs) > MaxBatchPurchases {
		return ErrInvalidBatchSize
	}
	seen := make(map[int64]bool, len(purchaseIDs))
	for _, id := range purchaseIDs {
		if seen[id] {
			return ErrDuplicatePurchaseID
		}
		seen[id] = true
	}
	return nil
}

// failureStatus maps an error of Claim or Decide to the status of its
// purchase. An error the service does not define is BatchError.
func failureStatus(err error) BatchStatus {
	switch {
	case errors.Is(err, ErrPurchaseNotFound):
		return BatchNotFound
	case errors.Is(err, ErrNotUnderReview):
		return BatchNotReview
	case errors.Is(err, ErrAlreadyDecided):
		return BatchAlreadyDecided
	case errors.Is(err, ErrNotClaimed):
		return BatchNotClaimed
	case errors.Is(err, ErrClaimedByOther):
		return BatchHeldByOther
	default:
		return BatchError
	}
}
