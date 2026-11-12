package trainingdata

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// DefaultMaturityDays is how old a purchase must be before it is labelled.
// Cardholders dispute weeks after a purchase, often after their statement
// arrives, and networks allow up to about 120 days; by 60 days most
// chargebacks that will ever come have come.
const DefaultMaturityDays = 60

// LabelOptions sets which purchases are labelled and when the labels are
// taken.
//
// AsOf is the time the export pretends to run at; chargebacks received later
// are ignored. The labels still depend on the chargebacks and review decisions
// recorded so far, so an export repeated later can differ.
//
// Only purchases at least MaturityDays older than AsOf are labelled. A newer
// purchase is left out whether or not it already has a fraud chargeback:
// keeping only the early fraud chargebacks would make the recent days look
// almost all fraud. MaturityDays 0 labels every purchase up to AsOf.
//
// Since and Until, when not zero, keep only purchases with Since <= event
// time < Until.
type LabelOptions struct {
	AsOf         time.Time
	MaturityDays int
	Since, Until time.Time
}

// LabelSummary counts the labels written and the purchases left out.
type LabelSummary struct {
	Fraud      int
	Legitimate int
	// Immature purchases are newer than the maturity window, so whether they
	// are fraud is not known yet.
	Immature int
}

// ChargebackLabelsSQL lists the purchases that were charged to the card: the
// allowed ones and the reviewed ones an analyst approved. Blocked, rejected
// and undecided purchases were never charged, so they can never get a
// chargeback and have no label. $1 is the as-of time, $2 the fraud reason
// codes, $3 the newest event time that counts as mature, and $4 and $5 the
// optional first and end event times.
const ChargebackLabelsSQL = `
SELECT p.transaction_id,
       EXISTS (
           SELECT 1
           FROM chargebacks c
           WHERE c.purchase_id = p.id
             AND c.reason_code = ANY($2::text[])
             AND c.received_at <= $1
       ) AS fraud,
       p.event_time <= $3 AS mature
FROM purchases p
JOIN decisions d ON d.purchase_id = p.id
WHERE p.event_time <= $1
  AND ($4::timestamptz IS NULL OR p.event_time >= $4)
  AND ($5::timestamptz IS NULL OR p.event_time < $5)
  AND CASE d.outcome
      WHEN 'allow' THEN true
      WHEN 'review' THEN coalesce((
          SELECT r.decision = 'approve'
          FROM review_resolutions r
          WHERE r.purchase_id = p.id
      ), false)
      ELSE false
  END
ORDER BY p.event_time, p.transaction_id`

// ExportChargebackLabels writes the labels CSV that ml/train.py reads with
// --labels: transaction_id,is_fraud. A mature charged purchase with a fraud
// chargeback received by AsOf is 1; one without is 0, including one with a
// non-fraud chargeback. Purchases inside the maturity window are left out.
func ExportChargebackLabels(ctx context.Context, conn platform.DBTX, opts LabelOptions, w io.Writer) (LabelSummary, error) {
	if err := opts.validate(); err != nil {
		return LabelSummary{}, err
	}
	matureBefore := opts.AsOf.UTC().AddDate(0, 0, -opts.MaturityDays)
	rows, err := conn.Query(ctx, ChargebackLabelsSQL, opts.AsOf.UTC(), models.FraudChargebackReasons(), matureBefore,
		optionalTime(opts.Since), optionalTime(opts.Until))
	if err != nil {
		return LabelSummary{}, fmt.Errorf("list chargeback labels: %w", err)
	}
	defer rows.Close()

	out := csv.NewWriter(w)
	if err := out.Write([]string{"transaction_id", "is_fraud"}); err != nil {
		return LabelSummary{}, err
	}
	var sum LabelSummary
	for rows.Next() {
		var r labelRow
		if err := rows.Scan(&r.TransactionID, &r.Fraud, &r.Mature); err != nil {
			return sum, fmt.Errorf("read chargeback label: %w", err)
		}
		label, known := sum.add(r)
		if !known {
			continue
		}
		if err := out.Write([]string{r.TransactionID, label}); err != nil {
			return sum, err
		}
	}
	if err := rows.Err(); err != nil {
		return sum, fmt.Errorf("list chargeback labels: %w", err)
	}
	out.Flush()
	return sum, out.Error()
}

func (o LabelOptions) validate() error {
	if o.MaturityDays < 0 {
		return fmt.Errorf("maturity window must not be negative, got %d days", o.MaturityDays)
	}
	if !o.Since.IsZero() && !o.Until.IsZero() && !o.Until.After(o.Since) {
		return errors.New("until must be after since")
	}
	return nil
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

type labelRow struct {
	TransactionID string
	Fraud, Mature bool
}

// add counts the purchase in the summary and returns its is_fraud value, or
// false when the purchase is inside the maturity window.
func (s *LabelSummary) add(r labelRow) (string, bool) {
	switch {
	case !r.Mature:
		s.Immature++
		return "", false
	case r.Fraud:
		s.Fraud++
		return "1", true
	default:
		s.Legitimate++
		return "0", true
	}
}
