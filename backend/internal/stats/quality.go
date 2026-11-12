package stats

import (
	"errors"
	"time"
)

var (
	// ErrQualityUnavailable means no evaluation pool was configured.
	ErrQualityUnavailable = errors.New("quality reporting is unavailable")
	// ErrQualityRunNotFound means no metadata or finalized answers identify the run.
	ErrQualityRunNotFound = errors.New("quality run not found")
)

// Quality describes saved scoring decisions with finalized evaluation answers.
// Rates are null when their denominator is zero; money remains exact as text.
type Quality struct {
	Source                 string          `json:"source"`
	RunID                  string          `json:"run_id"`
	GeneratedAt            time.Time       `json:"generated_at"`
	LabelledDecisions      int64           `json:"labelled_decisions"`
	TruePositives          int64           `json:"true_positives"`
	FalsePositives         int64           `json:"false_positives"`
	TrueNegatives          int64           `json:"true_negatives"`
	FalseNegatives         int64           `json:"false_negatives"`
	Precision              *float64        `json:"precision"`
	Recall                 *float64        `json:"recall"`
	FalsePositiveRate      *float64        `json:"false_positive_rate"`
	FraudValueFlaggedMinor string          `json:"fraud_value_flagged_minor"`
	FraudValueAllowedMinor string          `json:"fraud_value_allowed_minor"`
	Coverage               QualityCoverage `json:"coverage"`
}

// QualityCoverage counts excluded intents without disclosing their answers.
// ScoredDecisions and RunStatus are known only for console-managed runs.
type QualityCoverage struct {
	Pending         int64   `json:"pending"`
	Quarantined     int64   `json:"quarantined"`
	ScoredDecisions *int64  `json:"scored_decisions"`
	RunStatus       *string `json:"run_status"`
}

// QualityRuns is a source-specific page, ordered by run ID ascending.
type QualityRuns struct {
	Source     string   `json:"source"`
	RunIDs     []string `json:"run_ids"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func (q *Quality) calculateRates() {
	q.LabelledDecisions = q.TruePositives + q.FalsePositives + q.TrueNegatives + q.FalseNegatives
	q.Precision = qualityRatio(q.TruePositives, q.TruePositives+q.FalsePositives)
	q.Recall = qualityRatio(q.TruePositives, q.TruePositives+q.FalseNegatives)
	q.FalsePositiveRate = qualityRatio(q.FalsePositives, q.FalsePositives+q.TrueNegatives)
}

func qualityRatio(numerator, denominator int64) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}
