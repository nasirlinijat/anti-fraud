package simulation

import (
	"context"
	"slices"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// BlockedPurchases returns up to limit purchases of the run that scoring
// blocked, newest first, with purchase IDs below before, or from the newest
// when before is 0. It also returns the cursor of the next page, which is 0
// on the last page, and models.ErrSimulationNotFound for an unknown run.
func (s *Service) BlockedPurchases(ctx context.Context, id string, before int64, limit int) ([]models.BlockedPurchase, int64, error) {
	if _, err := s.store.Get(ctx, id); err != nil {
		return nil, 0, err
	}
	items, err := s.store.BlockedPurchases(ctx, id, before, limit+1)
	if err != nil {
		return nil, 0, err
	}
	var next int64
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].PurchaseID
	}
	for i := range items {
		items[i].Reason = blockReason(items[i].FiredRules)
	}
	return items, next, nil
}

// blockReason tells a blocklist match from a rule score at the block
// threshold. Scoring records a blocklist match as its only fired rule, and
// the model never blocks.
func blockReason(fired []scoring.FiredRule) string {
	if slices.ContainsFunc(fired, func(r scoring.FiredRule) bool { return r.Name == scoring.RuleBlocklist }) {
		return models.BlockReasonBlocklist
	}
	return models.BlockReasonRules
}
