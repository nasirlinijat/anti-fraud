package models

import (
	"errors"
	"time"
)

// ChargebackReason is the reason code a processor reports with a chargeback.
type ChargebackReason string

const (
	// ChargebackFraudCardNotPresent means the cardholder says they did not
	// authorise an online purchase made with their card details.
	ChargebackFraudCardNotPresent ChargebackReason = "fraud_card_not_present"
	// ChargebackFraudCardAbsentOther is any other card-absent fraud, for
	// example a purchase from a taken-over account with a saved card.
	ChargebackFraudCardAbsentOther ChargebackReason = "fraud_card_absent_other"
	// ChargebackNotRecognised means the cardholder does not recognise the
	// purchase on the statement. It is often a family member or a forgotten
	// purchase rather than fraud.
	ChargebackNotRecognised      ChargebackReason = "not_recognised"
	ChargebackProductNotReceived ChargebackReason = "product_not_received"
	ChargebackDuplicate          ChargebackReason = "duplicate"
	ChargebackOther              ChargebackReason = "other"
)

// ChargebackReasons lists every reason code the webhook accepts.
var ChargebackReasons = []ChargebackReason{
	ChargebackFraudCardNotPresent, ChargebackFraudCardAbsentOther, ChargebackNotRecognised,
	ChargebackProductNotReceived, ChargebackDuplicate, ChargebackOther,
}

// Fraud reports whether a chargeback with this reason counts as fraud in the
// training labels. Only the two fraud codes do: not_recognised is too often a
// relative or a forgotten purchase, and the others are service disputes.
func (r ChargebackReason) Fraud() bool {
	return r == ChargebackFraudCardNotPresent || r == ChargebackFraudCardAbsentOther
}

// FraudChargebackReasons lists the reason codes that count as fraud.
func FraudChargebackReasons() []string {
	var codes []string
	for _, r := range ChargebackReasons {
		if r.Fraud() {
			codes = append(codes, string(r))
		}
	}
	return codes
}

// Chargeback is a chargeback reported by the processor for a scored purchase.
// AmountMinor is in minor units and ReceivedAt, when the bank received the
// dispute, is in UTC.
type Chargeback struct {
	ChargebackID  string
	TransactionID string
	Reason        ChargebackReason
	AmountMinor   int64
	ReceivedAt    time.Time
}

// StoredChargeback is a recorded chargeback.
type StoredChargeback struct {
	Chargeback
	RecordedAt time.Time
}

// ChargedPurchase is the part of a saved purchase that a chargeback is checked
// against.
type ChargedPurchase struct {
	ID          int64
	EventTime   time.Time
	AmountMinor int64
}

// ErrPurchaseNotFound means no purchase was scored with the transaction ID.
var ErrPurchaseNotFound = errors.New("no purchase with this transaction ID")

// ErrChargebackConflict means a chargeback with the same ID was recorded with
// different content.
var ErrChargebackConflict = errors.New("a chargeback with this ID was recorded with different content")
