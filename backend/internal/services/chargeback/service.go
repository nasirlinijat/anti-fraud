// Package chargeback records the chargebacks a processor reports through its
// webhook. Chargebacks become training labels for the fraud model in the
// offline export; nothing in scoring, model inputs, rules or reviews reads them.
package chargeback

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const (
	maxIDRunes = 200
	// maxClockSkew matches the score endpoint: a bank's receive time may be
	// slightly ahead of the service clock, but not further.
	maxClockSkew = 5 * time.Minute
)

// Store reads the charged purchase and records chargebacks.
type Store interface {
	Purchase(ctx context.Context, transactionID string) (models.ChargedPurchase, error)
	Insert(ctx context.Context, purchaseID int64, c models.Chargeback) (models.StoredChargeback, bool, error)
}

// ValidationError lists the problems found in a chargeback, keyed by field name.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return "invalid chargeback"
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// Record validates the chargeback and records it. It returns the stored
// chargeback and true when it was recorded now, or the earlier chargeback and
// false for a repeated delivery with the same content. It returns a
// *ValidationError for an invalid chargeback, models.ErrPurchaseNotFound for
// an unknown transaction, and models.ErrChargebackConflict for an ID recorded
// with different content.
func (s *Service) Record(ctx context.Context, c models.Chargeback, now time.Time) (models.StoredChargeback, bool, error) {
	c, fields := normalize(c, now)
	if len(fields) > 0 {
		return models.StoredChargeback{}, false, &ValidationError{Fields: fields}
	}
	purchase, err := s.store.Purchase(ctx, c.TransactionID)
	if err != nil {
		return models.StoredChargeback{}, false, err
	}
	if fields := againstPurchase(c, purchase); len(fields) > 0 {
		return models.StoredChargeback{}, false, &ValidationError{Fields: fields}
	}
	stored, created, err := s.store.Insert(ctx, purchase.ID, c)
	if err != nil {
		return models.StoredChargeback{}, false, err
	}
	if !created && !sameContent(stored.Chargeback, c) {
		return models.StoredChargeback{}, false, models.ErrChargebackConflict
	}
	return stored, created, nil
}

// normalize trims the IDs, converts the receive time to UTC at PostgreSQL's
// microsecond precision so a repeated delivery compares equal to the stored
// one, and returns the problems found, keyed by field name.
func normalize(in models.Chargeback, now time.Time) (models.Chargeback, map[string]string) {
	fields := map[string]string{}
	c := models.Chargeback{
		ChargebackID:  requiredID(fields, "chargeback_id", in.ChargebackID),
		TransactionID: requiredID(fields, "transaction_id", in.TransactionID),
		Reason:        models.ChargebackReason(strings.TrimSpace(string(in.Reason))),
		AmountMinor:   in.AmountMinor,
		ReceivedAt:    in.ReceivedAt.UTC().Truncate(time.Microsecond),
	}
	if !slices.Contains(models.ChargebackReasons, c.Reason) {
		fields["reason_code"] = "must be one of " + reasonList()
	}
	if c.AmountMinor <= 0 {
		fields["amount_minor"] = "must be a positive integer in minor units"
	}
	switch {
	case in.ReceivedAt.IsZero():
		fields["received_at"] = "is required"
	case c.ReceivedAt.Sub(now) > maxClockSkew:
		fields["received_at"] = fmt.Sprintf("must not be more than %d minutes in the future", int(maxClockSkew.Minutes()))
	}
	return c, fields
}

func requiredID(fields map[string]string, name, value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		fields[name] = "is required"
	case strings.ContainsFunc(value, unicode.IsControl):
		fields[name] = "must not contain control characters"
	case utf8.RuneCountInString(value) > maxIDRunes:
		fields[name] = fmt.Sprintf("must be at most %d characters", maxIDRunes)
	}
	return value
}

func reasonList() string {
	names := make([]string, len(models.ChargebackReasons))
	for i, r := range models.ChargebackReasons {
		names[i] = string(r)
	}
	return strings.Join(names, ", ")
}

// againstPurchase returns the problems that only show against the charged
// purchase: a chargeback cannot be larger than the purchase or received
// before it happened.
func againstPurchase(c models.Chargeback, p models.ChargedPurchase) map[string]string {
	fields := map[string]string{}
	if c.AmountMinor > p.AmountMinor {
		fields["amount_minor"] = "must not exceed the purchase amount"
	}
	if c.ReceivedAt.Before(p.EventTime) {
		fields["received_at"] = "must not be before the purchase event time"
	}
	return fields
}

func sameContent(a, b models.Chargeback) bool {
	return a.ChargebackID == b.ChargebackID && a.TransactionID == b.TransactionID && a.Reason == b.Reason &&
		a.AmountMinor == b.AmountMinor && a.ReceivedAt.Equal(b.ReceivedAt)
}
