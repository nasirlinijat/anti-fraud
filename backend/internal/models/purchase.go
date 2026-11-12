package models

import (
	"errors"
	"net/netip"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// Purchase is a scored purchase as received in the score request. Optional
// fields are nil when the request omits them.
type Purchase struct {
	TransactionID        string
	EventTime            time.Time
	AmountMinor          int64
	PlayerID             string
	CardToken            string
	CardIssuer           *string
	CardNetwork          *string
	CardType             *string
	CardCountry          *string
	CardCategory         *string
	ProductCode          *string
	BillingRegion        *string
	BillingCountry       *string
	BillingDistance      *float64
	EmailDomain          *string
	RecipientEmailDomain *string
	DeviceType           *string
	DeviceName           *string
	DeviceID             *string
	IPAddress            *netip.Addr
	IPCountry            *string
	AccountCreatedAt     *time.Time
	ClientOS             *string
	ClientBrowser        *string
	ScreenResolution     *string
	// The gateway verification results are rule inputs only, never model
	// inputs, so FeatureInput leaves them out.
	CVVResult     *scoring.CVVResult
	AVSResult     *scoring.AVSResult
	ThreeDSResult *scoring.ThreeDSResult
}

// Decision is the scoring result saved with a purchase.
type Decision struct {
	RulesetVersion int
	Result         scoring.Result
	// ModelInputs maps each model feature name to its value. It is saved even
	// when no model is loaded, so the inputs can be exported for training.
	ModelInputs map[string]float64
	// ModelScore is nil when no model is loaded. It is the score after blending
	// with the player's earlier scores.
	ModelScore *float64
	// ModelScoreUnblended is the main model's own score, before blending and
	// before combining with other models.
	ModelScoreUnblended *float64
	// ModelVersion is the version of the model that gave ModelScore. It is nil
	// when no model is loaded.
	ModelVersion *string
	// Latency is how long scoring took, from the start of the request until
	// the decision was ready to save. It is saved in microseconds.
	Latency time.Duration
	// FeatureHistory is the history the model inputs were computed from. Save
	// uses it to add the purchase to the feature running totals.
	FeatureHistory features.History
	// IPIntel is the IP intelligence lookup the rules saw. It is nil when no
	// lookup was made: the provider is off or the purchase had no IP address.
	// It is evidence and a rule input only, never a model input.
	IPIntel *ipintel.Result
}

// ErrDuplicateTransaction means a purchase with the same transaction ID is already saved.
var ErrDuplicateTransaction = errors.New("transaction ID is already saved")

// FeatureInput returns the request fields the model features use.
func (p Purchase) FeatureInput() features.Input {
	return features.Input{
		TransactionID:        p.TransactionID,
		EventTime:            p.EventTime,
		AmountMinor:          p.AmountMinor,
		PlayerID:             p.PlayerID,
		CardToken:            p.CardToken,
		CardIssuer:           p.CardIssuer,
		CardNetwork:          p.CardNetwork,
		CardType:             p.CardType,
		CardCountry:          p.CardCountry,
		CardCategory:         p.CardCategory,
		ProductCode:          p.ProductCode,
		BillingRegion:        p.BillingRegion,
		BillingCountry:       p.BillingCountry,
		BillingDistance:      p.BillingDistance,
		EmailDomain:          p.EmailDomain,
		RecipientEmailDomain: p.RecipientEmailDomain,
		DeviceType:           p.DeviceType,
		DeviceName:           p.DeviceName,
		DeviceID:             p.DeviceID,
		IPCountry:            p.IPCountry,
		AccountCreatedAt:     p.AccountCreatedAt,
		ClientOS:             p.ClientOS,
		ClientBrowser:        p.ClientBrowser,
		ScreenResolution:     p.ScreenResolution,
	}
}

// Verification returns the gateway verification results the rules check. A
// result the request omitted stays empty.
func (p Purchase) Verification() scoring.PaymentVerification {
	var v scoring.PaymentVerification
	if p.CVVResult != nil {
		v.CVV = *p.CVVResult
	}
	if p.AVSResult != nil {
		v.AVS = *p.AVSResult
	}
	if p.ThreeDSResult != nil {
		v.ThreeDS = *p.ThreeDSResult
	}
	return v
}
