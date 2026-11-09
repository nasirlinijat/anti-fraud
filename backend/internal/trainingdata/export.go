package trainingdata

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
)

// PageSize is how many decisions Export reads per query.
var PageSize int32 = 5000

// Summary counts what Export wrote and left out.
type Summary struct {
	Rows int
	// Skipped decisions have model inputs for another feature list, for
	// example from an older model or saved before inputs were recorded.
	Skipped int
}

// Export writes one CSV row per decision whose saved model inputs have exactly
// the given features, in (event_time, transaction_id) order. Columns:
// transaction_id, event_time (RFC 3339, UTC), player_id, model_score (empty
// without a model), then the features in the given order.
func Export(ctx context.Context, conn db.DBTX, featureNames []string, w io.Writer) (Summary, error) {
	q := db.New(conn)
	out := csv.NewWriter(w)
	header := append([]string{"transaction_id", "event_time", "player_id", "model_score"}, featureNames...)
	if err := out.Write(header); err != nil {
		return Summary{}, err
	}

	var sum Summary
	after := pgtype.Timestamptz{Time: time.Time{}, Valid: true}
	afterID := ""
	record := make([]string, len(header))
	for {
		rows, err := q.ListModelInputs(ctx, db.ListModelInputsParams{AfterTime: after, AfterID: afterID, PageSize: PageSize})
		if err != nil {
			return sum, fmt.Errorf("list model inputs: %w", err)
		}
		for _, r := range rows {
			var inputs map[string]float64
			if err := json.Unmarshal(r.ModelInputs, &inputs); err != nil {
				return sum, fmt.Errorf("decode model inputs of %s: %w", r.TransactionID, err)
			}
			if len(inputs) != len(featureNames) {
				sum.Skipped++
				continue
			}
			record[0] = r.TransactionID
			record[1] = r.EventTime.Time.UTC().Format(time.RFC3339)
			record[2] = r.PlayerID
			record[3] = ""
			if r.ModelScore.Valid {
				record[3] = strconv.FormatFloat(r.ModelScore.Float64, 'g', -1, 64)
			}
			complete := true
			for i, name := range featureNames {
				value, ok := inputs[name]
				if !ok {
					complete = false
					break
				}
				record[4+i] = strconv.FormatFloat(value, 'g', -1, 64)
			}
			if !complete {
				sum.Skipped++
				continue
			}
			if err := out.Write(record); err != nil {
				return sum, err
			}
			sum.Rows++
		}
		if len(rows) < int(PageSize) {
			break
		}
		last := rows[len(rows)-1]
		after, afterID = last.EventTime, last.TransactionID
	}
	out.Flush()
	return sum, out.Error()
}
