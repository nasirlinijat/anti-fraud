package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

//go:generate mockgen -destination=mocks/mock_store.go -package=mocks . Store

const PageSize = 100

var (
	ErrInvalidDecision        = errors.New("decision must be approve or reject")
	ErrBlockDeviceNeedsReject = errors.New("block_device is only allowed with a reject decision")
	ErrPurchaseNotFound       = errors.New("purchase not found")
	ErrNotUnderReview         = errors.New("purchase was not scored for review")
	ErrAlreadyDecided         = errors.New("purchase has already been decided")
	ErrNotClaimed             = errors.New("purchase must be claimed by you before you decide it")
	ErrClaimedByOther         = errors.New("purchase is claimed by another account")
)

type Store interface {
	Queue(ctx context.Context, limit int, order models.QueueOrder, after *models.QueueCursor, claimedBy *string) ([]models.QueueItem, error)
	Case(ctx context.Context, purchaseID int64) (models.CaseItem, error)
	Resolve(ctx context.Context, d models.ReviewDecision) (models.Resolution, error)
	Claim(ctx context.Context, purchaseID int64, username string) (models.Claim, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

type Page struct {
	Items []models.QueueItem
	Next  *models.QueueCursor
}

// Queue returns a page of open reviews. When claimedBy is set, the page holds
// only the purchases claimed by that username.
func (s *Service) Queue(ctx context.Context, order models.QueueOrder, after *models.QueueCursor, claimedBy *string) (Page, error) {
	items, err := s.store.Queue(ctx, PageSize+1, order, after, claimedBy)
	if err != nil {
		return Page{}, err
	}
	if len(items) <= PageSize {
		return Page{Items: items}, nil
	}
	items = items[:PageSize]
	next := CursorFor(order, items[PageSize-1])
	return Page{Items: items, Next: &next}, nil
}

// CursorFor matches the ascending score keys in the queue SQL, including the
// key for a missing model score.
func CursorFor(order models.QueueOrder, item models.QueueItem) models.QueueCursor {
	key := -float64(item.RuleScore)
	if order == models.QueueByModelScore {
		key = 1
		if item.ModelScore != nil {
			key = -*item.ModelScore
		}
	}
	return models.QueueCursor{DecidedAt: item.DecidedAt, PurchaseID: item.PurchaseID, ScoreKey: key}
}

func (s *Service) Case(ctx context.Context, purchaseID int64) (models.CaseItem, error) {
	return s.store.Case(ctx, purchaseID)
}

// Decide saves the decision of an analyst with a key on a purchase awaiting
// review. Each purchase gets one decision; a rejection can also blocklist the
// purchase's device in the same transaction.
func (s *Service) Decide(ctx context.Context, purchaseID int64, decision, analystID string, blockDevice bool) (models.Resolution, error) {
	return s.decide(ctx, models.ReviewDecision{
		PurchaseID: purchaseID, Decision: decision, AnalystID: analystID, BlockDevice: blockDevice,
	})
}

// DecideClaimed saves the decision of a signed-in account, which must hold
// the claim on the purchase. It returns ErrNotClaimed otherwise.
func (s *Service) DecideClaimed(ctx context.Context, purchaseID int64, decision, username string, blockDevice bool) (models.Resolution, error) {
	return s.decide(ctx, models.ReviewDecision{
		PurchaseID: purchaseID, Decision: decision, AnalystID: username, BlockDevice: blockDevice, ClaimedBy: &username,
	})
}

func (s *Service) decide(ctx context.Context, d models.ReviewDecision) (models.Resolution, error) {
	if d.Decision != "approve" && d.Decision != "reject" {
		return models.Resolution{}, ErrInvalidDecision
	}
	if d.BlockDevice && d.Decision != "reject" {
		return models.Resolution{}, ErrBlockDeviceNeedsReject
	}
	result, err := s.store.Resolve(ctx, d)
	if err != nil {
		return models.Resolution{}, err
	}
	switch result.Status {
	case models.ResolutionSaved:
		return result, nil
	case models.ResolutionNotFound:
		return models.Resolution{}, ErrPurchaseNotFound
	case models.ResolutionNotReview:
		return models.Resolution{}, ErrNotUnderReview
	case models.ResolutionAlreadyResolved:
		return models.Resolution{}, ErrAlreadyDecided
	case models.ResolutionNotClaimed:
		return models.Resolution{}, ErrNotClaimed
	default:
		return models.Resolution{}, fmt.Errorf("unexpected review decision status %q", result.Status)
	}
}

// Claim gives a purchase awaiting review to the signed-in account, so that
// only that account can decide it. Claiming a purchase the account already
// holds succeeds again with the original claim.
func (s *Service) Claim(ctx context.Context, purchaseID int64, username string) (models.Claim, error) {
	claim, err := s.store.Claim(ctx, purchaseID, username)
	if err != nil {
		return models.Claim{}, err
	}
	switch claim.Status {
	case models.ClaimSaved, models.ClaimAlreadyYours:
		return claim, nil
	case models.ClaimNotFound:
		return models.Claim{}, ErrPurchaseNotFound
	case models.ClaimNotReview:
		return models.Claim{}, ErrNotUnderReview
	case models.ClaimAlreadyResolved:
		return models.Claim{}, ErrAlreadyDecided
	case models.ClaimHeldByOther:
		return models.Claim{}, ErrClaimedByOther
	default:
		return models.Claim{}, fmt.Errorf("unexpected review claim status %q", claim.Status)
	}
}
