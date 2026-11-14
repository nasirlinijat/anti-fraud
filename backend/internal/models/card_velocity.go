package models

import "time"

// CardVelocityReferenceLimit bounds the number of purchase IDs saved with a score.
const CardVelocityReferenceLimit = 50

// CardVelocityHistory holds a count and, when it fits, every ID counted in the same read.
type CardVelocityHistory struct {
	Count       int
	PurchaseIDs []int64
}

// CardVelocitySnapshot is the observation saved when the card velocity rule fired.
type CardVelocitySnapshot struct {
	Count       int       `json:"count"`
	Limit       int64     `json:"limit"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	PurchaseIDs []int64   `json:"purchase_ids,omitempty"`
}

// CardVelocityEvidence adds available timeline details to the saved observation.
type CardVelocityEvidence struct {
	Snapshot         CardVelocitySnapshot
	RecordsAvailable bool
	ContextTruncated bool
	Purchases        []CardVelocityPurchase
}

// CardVelocityPurchase describes a transaction in the saved rule window.
type CardVelocityPurchase struct {
	TransactionID         string
	EventTime             time.Time
	AmountMinor           int64
	Contributed           bool
	RecordedAfterDecision bool
}
