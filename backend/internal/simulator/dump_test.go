package simulator_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

func TestFraudMarksTheFraudScenarios(t *testing.T) {
	fraud := map[simulator.Pattern]bool{
		simulator.PatternCardTesting:     true,
		simulator.PatternAccountTakeover: true,
		simulator.PatternNewAccountSpend: true,
		simulator.PatternStolenCard:      true,
		simulator.PatternLowAndSlow:      true,
		simulator.PatternSlowCardTesting: true,
		simulator.PatternQuietTakeover:   true,
		simulator.PatternDeviceFarm:      true,
	}
	for _, p := range simulator.Patterns {
		if got := p.Fraud(); got != fraud[p] {
			t.Errorf("%s: Fraud() = %v, want %v", p, got, fraud[p])
		}
	}
}

func TestWriteJSONLWritesEveryPurchaseWithItsLabel(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.5
	purchases := generate(t, cfg, 300)

	var out bytes.Buffer
	if err := simulator.WriteJSONL(&out, purchases); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}

	type line struct {
		Pattern simulator.Pattern `json:"pattern"`
		Fraud   *bool             `json:"is_fraud"`
		Request simulator.Request `json:"request"`
	}
	scanner := bufio.NewScanner(&out)
	count, frauds := 0, 0
	for scanner.Scan() {
		var got line
		if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
			t.Fatalf("line %d: %v", count+1, err)
		}
		want := purchases[count]
		if got.Pattern != want.Pattern || got.Request.TransactionID != want.Request.TransactionID ||
			got.Request.AmountMinor != want.Request.AmountMinor || !got.Request.EventTime.Equal(want.Request.EventTime) {
			t.Fatalf("line %d = %s %s, want %s %s", count+1, got.Pattern, got.Request.TransactionID, want.Pattern, want.Request.TransactionID)
		}
		if got.Fraud == nil || *got.Fraud != want.Pattern.Fraud() {
			t.Fatalf("line %d: is_fraud = %v, want %v", count+1, got.Fraud, want.Pattern.Fraud())
		}
		if *got.Fraud {
			frauds++
		}
		count++
	}
	if count != len(purchases) {
		t.Fatalf("wrote %d lines, want %d", count, len(purchases))
	}
	if frauds == 0 || frauds == count {
		t.Fatalf("%d of %d purchases are fraud, want a mix", frauds, count)
	}
}

func TestWriteAccountEventsJSONLWritesEveryEventInTimeOrder(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.5
	purchases := generate(t, cfg, 300)
	want := 0
	for _, p := range purchases {
		want += len(p.AccountEvents)
	}

	var out bytes.Buffer
	if err := simulator.WriteAccountEventsJSONL(&out, purchases); err != nil {
		t.Fatalf("WriteAccountEventsJSONL: %v", err)
	}

	scanner := bufio.NewScanner(&out)
	count, last := 0, time.Time{}
	for scanner.Scan() {
		var got simulator.AccountEvent
		if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
			t.Fatalf("line %d: %v", count+1, err)
		}
		if got.EventID == "" || got.PlayerID == "" || got.EventTime.Before(last) {
			t.Fatalf("line %d = %+v, after an event at %v", count+1, got, last)
		}
		last = got.EventTime
		count++
	}
	if count != want || want == 0 {
		t.Fatalf("wrote %d events, want %d", count, want)
	}
}

func TestAccountEventsPathIsNextToTheDump(t *testing.T) {
	if got := simulator.AccountEventsPath("data/game/world1.jsonl"); got != "data/game/world1.account_events.jsonl" {
		t.Errorf("AccountEventsPath = %q", got)
	}
}
