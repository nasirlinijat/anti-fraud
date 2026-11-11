package simulator

import (
	"bufio"
	"encoding/json"
	"io"
)

// Fraud reports whether purchases of the pattern are fraud. It is the answer
// key for generated traffic and is never sent to the score API.
func (p Pattern) Fraud() bool {
	switch p {
	case PatternCardTesting, PatternAccountTakeover, PatternNewAccountSpend, PatternStolenCard, PatternLowAndSlow:
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
