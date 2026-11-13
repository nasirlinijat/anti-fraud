package simulator

import (
	"bufio"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

// Fraud reports whether purchases of the pattern are fraud. It is the answer
// key for generated traffic and is never sent to the score API.
func (p Pattern) Fraud() bool {
	switch p {
	case PatternCardTesting, PatternAccountTakeover, PatternNewAccountSpend, PatternStolenCard, PatternLowAndSlow,
		PatternSlowCardTesting, PatternQuietTakeover, PatternDeviceFarm:
		return true
	}
	return false
}

type labelledPurchase struct {
	Pattern Pattern `json:"pattern"`
	Fraud   bool    `json:"is_fraud"`
	Request Request `json:"request"`
}

// WriteJSONL writes one JSON object per line for every purchase: the request
// in the API format with its pattern and fraud label. The file is training
// data for the fraud model; the labels in it never enter score requests.
func WriteJSONL(w io.Writer, purchases []Purchase) error {
	buffered := bufio.NewWriter(w)
	enc := json.NewEncoder(buffered)
	for _, p := range purchases {
		if err := enc.Encode(labelledPurchase{Pattern: p.Pattern, Fraud: p.Pattern.Fraud(), Request: p.Request}); err != nil {
			return err
		}
	}
	return buffered.Flush()
}

// AccountEventsPath returns the file that the account events of the dump at
// dumpPath are written to, next to it: world1.jsonl has
// world1.account_events.jsonl. ml/features.py reads the same name.
func AccountEventsPath(dumpPath string) string {
	return strings.TrimSuffix(dumpPath, filepath.Ext(dumpPath)) + ".account_events.jsonl"
}

// WriteAccountEventsJSONL writes the account events of every purchase, one
// POST /account-events request per line, in event time order. Like the
// purchases they are training data; they carry no label.
func WriteAccountEventsJSONL(w io.Writer, purchases []Purchase) error {
	var events []AccountEvent
	for _, p := range purchases {
		events = append(events, p.AccountEvents...)
	}
	slices.SortStableFunc(events, func(a, b AccountEvent) int { return a.EventTime.Compare(b.EventTime) })
	buffered := bufio.NewWriter(w)
	enc := json.NewEncoder(buffered)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return buffered.Flush()
}
