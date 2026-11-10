package stats

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
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

func readDecisionSeries(ctx context.Context, q *db.Queries, window Window, overview db.DecisionOverviewRow) (DecisionSeries, error) {
	width, start := window.BucketWidth, window.Since
	if width == 0 {
		start = overview.FirstDecisionAt.Time
		width = time.Minute
		if overview.Total > 0 {
			width = allTimeBucketWidth(start, window.Until)
		}
	}
	series := DecisionSeries{BucketSeconds: int64(width / time.Second), Buckets: []DecisionBucket{}}
	if overview.Total == 0 {
		return series, nil
	}

	rows, err := q.CountDecisionBuckets(ctx, db.CountDecisionBucketsParams{
		BucketWidth: pgtype.Interval{Microseconds: width.Microseconds(), Valid: true},
		Since:       pgtype.Timestamptz{Time: window.Since, Valid: true},
		Until:       pgtype.Timestamptz{Time: window.Until, Valid: true},
	})
	if err != nil {
		return DecisionSeries{}, err
	}
	for at := bucketStart(start, width); at.Before(window.Until); at = at.Add(width) {
		bucket := DecisionBucket{Start: at}
		if len(rows) > 0 && rows[0].BucketStart.Time.Equal(at) {
			bucket.Allow, bucket.Review, bucket.Block = rows[0].Allowed, rows[0].Reviewed, rows[0].Blocked
			rows = rows[1:]
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
