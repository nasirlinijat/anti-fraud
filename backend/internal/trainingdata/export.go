package trainingdata

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
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
func Export(ctx context.Context, conn platform.DBTX, featureNames []string, w io.Writer) (Summary, error) {
	out := csv.NewWriter(w)
	header := append([]string{"transaction_id", "event_time", "player_id", "model_score"}, featureNames...)
	if err := out.Write(header); err != nil {
		return Summary{}, err
	}

	var sum Summary
	var after time.Time
	afterID := ""
	record := make([]string, len(header))
	for {
		rows, err := listModelInputs(ctx, conn, after, afterID)
		if err != nil {
			return sum, fmt.Errorf("list model inputs: %w", err)
		}
		for _, r := range rows {
			ok, err := fillRecord(record, r, featureNames)
			if err != nil {
				return sum, err
			}
			if !ok {
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

// fillRecord writes the columns of one decision into record. It returns false
// when the saved model inputs are not exactly the given features.
func fillRecord(record []string, r modelInputsRow, featureNames []string) (bool, error) {
	var inputs map[string]float64
	if err := json.Unmarshal(r.ModelInputs, &inputs); err != nil {
		return false, fmt.Errorf("decode model inputs of %s: %w", r.TransactionID, err)
	}
	if len(inputs) != len(featureNames) {
		return false, nil
	}
	record[0] = r.TransactionID
	record[1] = r.EventTime.UTC().Format(time.RFC3339)
	record[2] = r.PlayerID
	record[3] = ""
	if r.ModelScore != nil {
		record[3] = strconv.FormatFloat(*r.ModelScore, 'g', -1, 64)
	}
	for i, name := range featureNames {
		value, ok := inputs[name]
		if !ok {
			return false, nil
		}
		record[4+i] = strconv.FormatFloat(value, 'g', -1, 64)
	}
	return true, nil
}

// ModelInputsPageSQL is a page of saved decisions after the given
// (event_time, transaction_id), in that order.
const ModelInputsPageSQL = `
SELECT p.transaction_id, p.event_time, p.player_id, d.model_inputs, d.model_score
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE (p.event_time, p.transaction_id) > ($1::timestamptz, $2::text)
ORDER BY p.event_time, p.transaction_id
LIMIT $3`

type modelInputsRow struct {
	TransactionID string
	EventTime     time.Time
	PlayerID      string
	ModelInputs   []byte
	ModelScore    *float64
}

func listModelInputs(ctx context.Context, conn platform.DBTX, after time.Time, afterID string) ([]modelInputsRow, error) {
	rows, err := conn.Query(ctx, ModelInputsPageSQL, after, afterID, PageSize)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (modelInputsRow, error) {
		var r modelInputsRow
		return r, row.Scan(&r.TransactionID, &r.EventTime, &r.PlayerID, &r.ModelInputs, &r.ModelScore)
	})
}
