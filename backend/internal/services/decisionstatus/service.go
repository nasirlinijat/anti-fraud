// Package decisionstatus reads saved risk decisions for integrating game servers.
package decisionstatus

import (
	"context"
	"fmt"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

//go:generate mockgen -destination=mocks/mock_store.go -package=mocks . Store

// Store reads one purchase without loading its investigation evidence.
type Store interface {
	Find(ctx context.Context, transactionID string) (models.SavedPurchaseStatus, error)
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// Lookup returns the original scoring outcome and the committed review state.
func (s *Service) Lookup(ctx context.Context, transactionID string) (models.DecisionStatus, error) {
	saved, err := s.store.Find(ctx, transactionID)
	if err != nil {
		return models.DecisionStatus{}, err
	}
	result := models.DecisionStatus{TransactionID: saved.TransactionID, Outcome: saved.Outcome, ReviewStatus: "not_required"}
	if saved.Outcome != scoring.OutcomeReview {
		return result, nil
	}
	result.ReviewStatus = "pending"
	if saved.ReviewDecision == nil {
		return result, nil
	}
	switch *saved.ReviewDecision {
	case "approve":
		result.ReviewStatus = "approved"
	case "reject":
		result.ReviewStatus = "rejected"
	default:
		return models.DecisionStatus{}, fmt.Errorf("invalid saved review decision")
	}
	if saved.ResolvedAt == nil {
		return models.DecisionStatus{}, fmt.Errorf("saved review has no resolution time")
	}
	at := saved.ResolvedAt.UTC()
	result.ResolvedAt = &at
	return result, nil
}
