package stats

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

type DecisionBucket struct {
	Start  time.Time `json:"start"`
	Allow  int64     `json:"allow"`
	Review int64     `json:"review"`
	Block  int64     `json:"block"`
}

type DecisionSeries struct {
	BucketSeconds int64            `json:"bucket_seconds"`
	Buckets       []DecisionBucket `json:"buckets"`
}

// countDecisionBuckets counts decisions by outcome in buckets of width $1
// aligned to the Unix epoch. Buckets without decisions have no row.
const countDecisionBuckets = `
SELECT
    date_bin($1::interval, decided_at, TIMESTAMPTZ '1970-01-01 00:00:00+00')::timestamptz AS bucket_start,
    count(*) FILTER (WHERE outcome = 'allow') AS allowed,
    count(*) FILTER (WHERE outcome = 'review') AS reviewed,
    count(*) FILTER (WHERE outcome = 'block') AS blocked
FROM decisions
WHERE decided_at >= $2::timestamptz
  AND decided_at < $3::timestamptz
GROUP BY bucket_start
ORDER BY bucket_start`

func readDecisionSeries(ctx context.Context, conn platform.DBTX, window Window, o overview) (DecisionSeries, error) {
	width, start := window.BucketWidth, window.Since
	if width == 0 {
		start = o.firstDecisionAt
		width = time.Minute
		if o.Total > 0 {
			width = allTimeBucketWidth(start, window.Until)
		}
	}
	series := DecisionSeries{BucketSeconds: int64(width / time.Second), Buckets: []DecisionBucket{}}
	if o.Total == 0 {
		return series, nil
	}

	rows, err := conn.Query(ctx, countDecisionBuckets,
		pgtype.Interval{Microseconds: width.Microseconds(), Valid: true}, window.Since, window.Until)
	if err != nil {
		return DecisionSeries{}, err
	}
	counted, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DecisionBucket, error) {
		var b DecisionBucket
		return b, row.Scan(&b.Start, &b.Allow, &b.Review, &b.Block)
	})
	if err != nil {
		return DecisionSeries{}, err
	}
	for at := bucketStart(start, width); at.Before(window.Until); at = at.Add(width) {
		bucket := DecisionBucket{Start: at}
		if len(counted) > 0 && counted[0].Start.Equal(at) {
			bucket.Allow, bucket.Review, bucket.Block = counted[0].Allow, counted[0].Review, counted[0].Block
			counted = counted[1:]
		}
		series.Buckets = append(series.Buckets, bucket)
	}
	return series, nil
}

const maxAllTimeBuckets = 48

func allTimeBucketWidth(first, until time.Time) time.Duration {
	const day = 24 * time.Hour
	last := until.Add(-time.Microsecond)
	fits := func(width time.Duration) bool {
		return bucketStart(last, width).Sub(bucketStart(first, width))/width < maxAllTimeBuckets
	}
	for _, width := range [...]time.Duration{time.Minute, 5 * time.Minute, time.Hour, 6 * time.Hour, day, 7 * day, 28 * day} {
		if fits(width) {
			return width
		}
	}
	// Start with the minimum whole-day width that could fit the span. Check
	// alignment too, since the two edge buckets can be partial.
	width := (last.Sub(first)/(maxAllTimeBuckets*day) + 1) * day
	for !fits(width) {
		width += day
	}
	return width
}

// bucketStart aligns with date_bin's Unix epoch origin and fixed-width stride.
func bucketStart(at time.Time, width time.Duration) time.Time {
	seconds := int64(width / time.Second)
	remainder := at.Unix() % seconds
	if remainder < 0 {
		remainder += seconds
	}
	return time.Unix(at.Unix()-remainder, 0).UTC()
}
