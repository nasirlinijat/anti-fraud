// Package mockbank stands in for the card networks: it decides which charged
// simulator purchases cardholders dispute, when the bank receives each
// dispute, and posts them to the chargeback webhook signed like a processor.
// The rates and delays are illustrative, not measured.
package mockbank

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

const (
	minDelayDays = 2
	// maxDelayDays is about the longest time card networks give a cardholder
	// to dispute a purchase.
	maxDelayDays = 120
	// delaySpread is the standard deviation of the log of the delay, so most
	// delays fall within about half to twice the median.
	delaySpread = 0.5
)

type weightedReason struct {
	reason models.ChargebackReason
	weight float64
}

// profile is how often purchases of one pattern are charged back, with which
// reasons, and the median delay before the bank receives the dispute.
type profile struct {
	rate            float64
	reasons         []weightedReason
	medianDelayDays float64
}

var (
	cardNotPresent = []weightedReason{{models.ChargebackFraudCardNotPresent, 1}}
	// Some victims of account takeover do not see the purchase as fraud on
	// their own account and dispute it as not recognised.
	takeover = []weightedReason{{models.ChargebackFraudCardAbsentOther, 0.6}, {models.ChargebackNotRecognised, 0.4}}
	// Friendly fraud: a legitimate purchase disputed by the cardholder,
	// sometimes even with a fraud reason code.
	friendly = []weightedReason{
		{models.ChargebackNotRecognised, 0.40}, {models.ChargebackFraudCardNotPresent, 0.25},
		{models.ChargebackProductNotReceived, 0.15}, {models.ChargebackDuplicate, 0.10}, {models.ChargebackOther, 0.10},
	}
	legitimate = profile{rate: 0.004, reasons: friendly, medianDelayDays: 40}
)

// profiles holds the patterns that differ from a legitimate purchase. Stolen
// card owners see the purchase on their statement and dispute most of it;
// card testing uses amounts so small that the card is usually cancelled before
// anyone disputes them.
var profiles = map[simulator.Pattern]profile{
	simulator.PatternStolenCard:      {rate: 0.70, reasons: cardNotPresent, medianDelayDays: 25},
	simulator.PatternNewAccountSpend: {rate: 0.60, reasons: cardNotPresent, medianDelayDays: 25},
	simulator.PatternDeviceFarm:      {rate: 0.50, reasons: cardNotPresent, medianDelayDays: 25},
	simulator.PatternLowAndSlow:      {rate: 0.45, reasons: cardNotPresent, medianDelayDays: 35},
	simulator.PatternAccountTakeover: {rate: 0.30, reasons: takeover, medianDelayDays: 30},
	simulator.PatternQuietTakeover:   {rate: 0.20, reasons: takeover, medianDelayDays: 35},
	simulator.PatternCardTesting:     {rate: 0.03, reasons: cardNotPresent, medianDelayDays: 20},
	simulator.PatternSlowCardTesting: {rate: 0.03, reasons: cardNotPresent, medianDelayDays: 20},
	simulator.PatternFamilyCard:      {rate: 0.03, reasons: []weightedReason{{models.ChargebackNotRecognised, 0.7}, {models.ChargebackFraudCardNotPresent, 0.3}}, medianDelayDays: 40},
}

// Plan returns every chargeback the purchases will ever get, in the order of
// the purchases. Each purchase draws from its own generator seeded by the seed
// and its transaction ID, so a purchase gets the same chargeback, with the
// same ID and receive time, whatever other purchases are planned with it.
func Plan(purchases []labels.SettledPurchase, seed uint64) []models.Chargeback {
	var planned []models.Chargeback
	for _, p := range purchases {
		if c, ok := chargeback(p, seed); ok {
			planned = append(planned, c)
		}
	}
	return planned
}

// ArrivedBy returns the planned chargebacks the bank has received by asOf.
func ArrivedBy(planned []models.Chargeback, asOf time.Time) []models.Chargeback {
	var arrived []models.Chargeback
	for _, c := range planned {
		if !c.ReceivedAt.After(asOf) {
			arrived = append(arrived, c)
		}
	}
	return arrived
}

func chargeback(p labels.SettledPurchase, seed uint64) (models.Chargeback, bool) {
	key := purchaseKey(p.TransactionID, seed)
	rng := rand.New(rand.NewPCG(seed, key))
	prof, ok := profiles[p.Pattern]
	if !ok {
		prof = legitimate
	}
	if rng.Float64() >= prof.rate {
		return models.Chargeback{}, false
	}
	reason := pickReason(rng, prof.reasons)
	delay := receiveDelay(rng, prof.medianDelayDays)
	return models.Chargeback{
		ChargebackID:  fmt.Sprintf("cb-%016x", key),
		TransactionID: p.TransactionID,
		Reason:        reason,
		AmountMinor:   p.AmountMinor,
		ReceivedAt:    p.EventTime.Add(delay).Truncate(time.Second).UTC(),
	}, true
}

func purchaseKey(transactionID string, seed uint64) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d:%s", seed, transactionID)
	return h.Sum64()
}

func pickReason(rng *rand.Rand, reasons []weightedReason) models.ChargebackReason {
	total := 0.0
	for _, r := range reasons {
		total += r.weight
	}
	pick := rng.Float64() * total
	for _, r := range reasons {
		if pick < r.weight {
			return r.reason
		}
		pick -= r.weight
	}
	return reasons[len(reasons)-1].reason
}

// receiveDelay draws the time from the purchase to the bank receiving the
// dispute from a log-normal distribution around the median, limited to the
// range in which banks accept disputes.
func receiveDelay(rng *rand.Rand, medianDays float64) time.Duration {
	days := medianDays * math.Exp(delaySpread*rng.NormFloat64())
	days = min(max(days, minDelayDays), maxDelayDays)
	return time.Duration(days * float64(24*time.Hour))
}
