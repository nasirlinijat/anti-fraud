package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const PageSize = 100

var (
	ErrInvalidDecision        = errors.New("decision must be approve or reject")
	ErrBlockDeviceNeedsReject = errors.New("block_device is only allowed with a reject decision")
	ErrPurchaseNotFound       = errors.New("purchase not found")
	ErrNotUnderReview         = errors.New("purchase was not scored for review")
	ErrAlreadyDecided         = errors.New("purchase has already been decided")
)

type Store interface {
	Queue(ctx context.Context, limit int, order models.QueueOrder, after *models.QueueCursor) ([]models.QueueItem, error)
	Case(ctx context.Context, purchaseID int64) (models.CaseItem, error)
	Resolve(ctx context.Context, purchaseID int64, decision, analystID string, blockDevice bool) (models.Resolution, error)
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

func (s *Service) Queue(ctx context.Context, order models.QueueOrder, after *models.QueueCursor) (Page, error) {
	items, err := s.store.Queue(ctx, PageSize+1, order, after)
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

// Decide saves an analyst's decision on a purchase awaiting review. Each
// purchase gets one decision; a rejection can also blocklist the purchase's
// device in the same transaction.
func (s *Service) Decide(ctx context.Context, purchaseID int64, decision, analystID string, blockDevice bool) (models.Resolution, error) {
	if decision != "approve" && decision != "reject" {
		return models.Resolution{}, ErrInvalidDecision
	}
	if blockDevice && decision != "reject" {
		return models.Resolution{}, ErrBlockDeviceNeedsReject
	}
	result, err := s.store.Resolve(ctx, purchaseID, decision, analystID, blockDevice)
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
	default:
		return models.Resolution{}, fmt.Errorf("unexpected review decision status %q", result.Status)
	}
}
