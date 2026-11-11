package models

import (
	"errors"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// QueueItem is a purchase marked for review.
type QueueItem struct {
	PurchaseID    int64
	TransactionID string
	EventTime     time.Time
	AmountMinor   int64
	PlayerID      string
	// DeviceID is nil when the purchase carried no device ID.
	DeviceID  *string
	RuleScore int
	// ModelScore is nil when the purchase was scored without a model.
	ModelScore *float64
	// ModelVersion is the version of the model that gave ModelScore.
	ModelVersion *string
	FiredRules   []scoring.FiredRule
	DecidedAt    time.Time
}

// CaseItem is a purchase marked for review, with the evidence behind the decision.
type CaseItem struct {
	QueueItem
	// RulesetVersion is the ruleset version that scored the purchase.
	RulesetVersion int
	// PlayerPurchases lists the player's recent purchases before this one, newest first.
	PlayerPurchases []PlayerPurchase
	// DeviceActivity summarizes activity on the same device, or is nil when the purchase had no device ID.
	DeviceActivity *DeviceActivity
}

// PlayerPurchase is an earlier purchase by the same player.
type PlayerPurchase struct {
	TransactionID string
	EventTime     time.Time
	AmountMinor   int64
	Outcome       scoring.Outcome
}

// DeviceActivity summarizes recent activity on the same device.
type DeviceActivity struct {
	Purchases     int64
	DistinctCards int64
	LastSeenAt    time.Time
}

// ErrReviewNotFound means no purchase under review has the given ID.
var ErrReviewNotFound = errors.New("review case not found")

type QueueOrder string

const (
	QueueByRuleScore  QueueOrder = "rule_score"
	QueueByModelScore QueueOrder = "model_score"
)

type QueueCursor struct {
	DecidedAt  time.Time
	PurchaseID int64
	ScoreKey   float64
}

type ResolutionStatus string

const (
	ResolutionSaved           ResolutionStatus = "saved"
	ResolutionNotFound        ResolutionStatus = "not_found"
	ResolutionNotReview       ResolutionStatus = "not_review"
	ResolutionAlreadyResolved ResolutionStatus = "already_resolved"
)

type Resolution struct {
	Status        ResolutionStatus
	ResolvedAt    time.Time
	DeviceBlocked bool
}
