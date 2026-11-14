package models

import (
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// SavedPurchaseStatus contains only saved decision and resolution data.
type SavedPurchaseStatus struct {
	TransactionID  string
	Outcome        scoring.Outcome
	ReviewDecision *string
	ResolvedAt     *time.Time
}

// DecisionStatus separates the original score from its eventual review result.
type DecisionStatus struct {
	TransactionID string
	Outcome       scoring.Outcome
	ReviewStatus  string
	ResolvedAt    *time.Time
}
