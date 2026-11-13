package review

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

//go:generate mockgen -destination=mocks/mock_store.go -package=mocks . Store

const PageSize = 100

const maxLinkedValueBytes = 256

type Store interface {
	Queue(ctx context.Context, limit int, order models.QueueOrder, after *models.QueueCursor, claimedBy *string) ([]models.QueueItem, error)
	Case(ctx context.Context, purchaseID int64) (models.CaseItem, error)
	Network(ctx context.Context, purchaseID int64) (models.CaseNetwork, error)
	NetworkPurchases(ctx context.Context, purchaseID int64, anchor models.BlocklistType, value string) (models.LinkedPurchases, error)
	LinkedReviews(ctx context.Context, purchaseID int64) (models.LinkedReviews, error)
	Resolve(ctx context.Context, d models.ReviewDecision) (models.Resolution, error)
	Claim(ctx context.Context, purchaseID int64, username string) (models.Claim, error)
	Assignments(ctx context.Context) ([]models.ReviewerWork, error)
	ReleaseClaim(ctx context.Context, purchaseID int64) (models.ReleaseStatus, error)
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

func (s *Service) Network(ctx context.Context, purchaseID int64) (models.CaseNetwork, error) {
	return s.store.Network(ctx, purchaseID)
}

// NetworkPurchases returns the purchases of a value linked to the purchase
// under review through one of its anchors. It returns ErrInvalidNetworkAnchor
// or ErrInvalidLinkedValue before reading anything.
func (s *Service) NetworkPurchases(ctx context.Context, purchaseID int64, anchor models.BlocklistType, value string) (models.LinkedPurchases, error) {
	if anchor != models.BlocklistDevice && anchor != models.BlocklistCard && anchor != models.BlocklistPlayer {
		return models.LinkedPurchases{}, ErrInvalidNetworkAnchor
	}
	if !isLinkedValue(value) {
		return models.LinkedPurchases{}, ErrInvalidLinkedValue
	}
	return s.store.NetworkPurchases(ctx, purchaseID, anchor, value)
}

// LinkedReviews returns the other purchases awaiting review that share the
// device, card, or player of the purchase under review.
func (s *Service) LinkedReviews(ctx context.Context, purchaseID int64) (models.LinkedReviews, error) {
	return s.store.LinkedReviews(ctx, purchaseID)
}

// isLinkedValue applies the limits of account event IDs, so that a query
// parameter cannot send an unbounded or unprintable value to the database.
func isLinkedValue(value string) bool {
	return value != "" && len(value) <= maxLinkedValueBytes && !strings.ContainsFunc(value, unicode.IsControl)
}

// Decide saves the decision of an analyst with a key on a purchase awaiting
// review. Each purchase gets one decision; a rejection can also blocklist the
// purchase's device, card and player in the same transaction.
func (s *Service) Decide(ctx context.Context, purchaseID int64, decision, analystID string, blocks models.ReviewBlocks) (models.Resolution, error) {
	return s.decide(ctx, models.ReviewDecision{
		PurchaseID: purchaseID, Decision: decision, AnalystID: analystID, Blocks: blocks,
	})
}

// DecideClaimed saves the decision of a signed-in account, which must hold
// the claim on the purchase. It returns ErrNotClaimed otherwise. Like Decide,
// a rejection can also blocklist the purchase's device, card and player.
func (s *Service) DecideClaimed(ctx context.Context, purchaseID int64, decision, username string, blocks models.ReviewBlocks) (models.Resolution, error) {
	return s.decide(ctx, models.ReviewDecision{
		PurchaseID: purchaseID, Decision: decision, AnalystID: username, Blocks: blocks, ClaimedBy: &username,
	})
}

func (s *Service) decide(ctx context.Context, d models.ReviewDecision) (models.Resolution, error) {
	if err := checkDecision(d.Decision, d.Blocks); err != nil {
		return models.Resolution{}, err
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

func checkDecision(decision string, blocks models.ReviewBlocks) error {
	if decision != "approve" && decision != "reject" {
		return ErrInvalidDecision
	}
	if blocks.Any() && decision != "reject" {
		return ErrBlockNeedsReject
	}
	return nil
}

// Claim gives a purchase awaiting review to the signed-in account, so that
// only that account can decide it. Claiming a purchase the account already
// holds succeeds again with the original claim. With ErrClaimedByOther it
// also returns the claim of the account that holds the purchase.
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
		return claim, ErrClaimedByOther
	default:
		return models.Claim{}, fmt.Errorf("unexpected review claim status %q", claim.Status)
	}
}

// Assignments returns each reviewer with their open claims and the number of
// purchases they decided.
func (s *Service) Assignments(ctx context.Context) ([]models.ReviewerWork, error) {
	return s.store.Assignments(ctx)
}

// ReleaseClaim returns a claimed purchase to the shared queue, so that any
// reviewer can claim it. It returns ErrNoOpenClaim for a purchase without a
// claim and ErrAlreadyDecided for one that was decided.
func (s *Service) ReleaseClaim(ctx context.Context, purchaseID int64) error {
	status, err := s.store.ReleaseClaim(ctx, purchaseID)
	if err != nil {
		return err
	}
	switch status {
	case models.ReleaseSaved:
		return nil
	case models.ReleaseNoOpenClaim:
		return ErrNoOpenClaim
	case models.ReleaseAlreadyResolved:
		return ErrAlreadyDecided
	default:
		return fmt.Errorf("unexpected claim release status %q", status)
	}
}
