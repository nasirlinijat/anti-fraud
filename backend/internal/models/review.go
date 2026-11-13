package models

import (
	"errors"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
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
	// ClaimedBy is the username holding the claim, or nil while the purchase is unclaimed.
	ClaimedBy *string
	// ClaimedAt is when the claim was made, or nil while the purchase is unclaimed.
	ClaimedAt *time.Time
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
	// The gateway verification results of the purchase are nil when the
	// request did not send them.
	CVVResult     *scoring.CVVResult
	AVSResult     *scoring.AVSResult
	ThreeDSResult *scoring.ThreeDSResult
	// AccountEvents lists the player's recent account events before this purchase, newest first.
	AccountEvents []AccountEvent
	// IPIntel is the IP intelligence lookup saved with the decision, or nil
	// when no lookup was made.
	IPIntel *ipintel.Result
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

// CaseNetwork lists the players and cards linked to a purchase under review
// through its device, card, and player within WindowDays of the purchase.
type CaseNetwork struct {
	WindowDays int
	// Anchors lists the purchase's device, when it has one, then its card and player.
	Anchors []NetworkAnchor
}

// NetworkAnchor is a value of the purchase under review and the entities that share it.
type NetworkAnchor struct {
	Type        BlocklistType
	Value       string
	Blocklisted bool
	// LinkedTotal counts every linked entity, including those left out of Linked.
	LinkedTotal int64
	// Linked lists the linked entities, worst status first.
	Linked []LinkedEntity
}

// LinkedEntity is a player or card that shares an anchor with the purchase under review.
type LinkedEntity struct {
	Type  BlocklistType
	Value string
	// Purchases counts its purchases that share the anchor within the window.
	Purchases int64
	Status    NetworkStatus
}

// LinkedPurchases lists the purchases of a linked entity that share an anchor
// with a purchase under review within WindowDays of that purchase.
type LinkedPurchases struct {
	// Anchor names the anchor of the purchase under review that links Value.
	Anchor     BlocklistType
	Value      string
	WindowDays int
	// Total counts every matching purchase, including those left out of Purchases.
	Total int64
	// Purchases lists the matching purchases, newest first.
	Purchases []LinkedPurchase
}

// LinkedPurchase is a purchase of a linked entity that shares the anchor.
type LinkedPurchase struct {
	PurchaseID    int64
	TransactionID string
	EventTime     time.Time
	AmountMinor   int64
	Outcome       scoring.Outcome
	// ReviewDecision is approve or reject, or nil when no analyst decided the purchase.
	ReviewDecision *string
	// ClaimedBy is the username holding the claim while the purchase awaits
	// review, or nil when nobody holds one.
	ClaimedBy *string
	// AwaitingReview is true for a purchase scored for review and not yet decided.
	AwaitingReview bool
}

// NetworkStatus is the worst thing known about a linked entity.
type NetworkStatus string

// Network statuses, worst first.
const (
	NetworkBlocklisted NetworkStatus = "blocklisted"
	NetworkRejected    NetworkStatus = "rejected"
	NetworkBlocked     NetworkStatus = "blocked"
	NetworkInReview    NetworkStatus = "in_review"
	NetworkClear       NetworkStatus = "clear"
)

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
	ResolutionNotClaimed      ResolutionStatus = "not_claimed"
)

// ReviewDecision is a decision on a purchase under review.
type ReviewDecision struct {
	PurchaseID  int64
	Decision    string
	AnalystID   string
	BlockDevice bool
	// ClaimedBy is the username that must hold the claim on the purchase, or
	// nil for a decision made with an analyst key, which needs no claim.
	ClaimedBy *string
}

type ClaimStatus string

const (
	ClaimSaved           ClaimStatus = "claimed"
	ClaimAlreadyYours    ClaimStatus = "already_yours"
	ClaimNotFound        ClaimStatus = "not_found"
	ClaimNotReview       ClaimStatus = "not_review"
	ClaimAlreadyResolved ClaimStatus = "already_resolved"
	ClaimHeldByOther     ClaimStatus = "held_by_other"
)

// ReviewerWork is what one account is working on: its open claims and the
// number of purchases it has decided.
type ReviewerWork struct {
	Username   string
	Role       Role
	Disabled   bool
	Decided    int64
	OpenClaims []OpenClaim
}

// OpenClaim is a claimed purchase that is still awaiting a decision.
type OpenClaim struct {
	PurchaseID  int64
	AmountMinor int64
	RuleScore   int
	// ModelScore is nil when the purchase was scored without a model.
	ModelScore *float64
	ClaimedAt  time.Time
}

type ReleaseStatus string

const (
	ReleaseSaved           ReleaseStatus = "released"
	ReleaseNoOpenClaim     ReleaseStatus = "no_open_claim"
	ReleaseAlreadyResolved ReleaseStatus = "already_resolved"
)

// Claim is the result of claiming a purchase under review.
type Claim struct {
	Status    ClaimStatus
	ClaimedBy string
	ClaimedAt time.Time
}

type Resolution struct {
	Status        ResolutionStatus
	ResolvedAt    time.Time
	DeviceBlocked bool
}
