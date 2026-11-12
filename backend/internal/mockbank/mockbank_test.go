package mockbank_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/mockbank"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

const secret = "mock-bank-secret-of-at-least-32-bytes"

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// purchases returns n purchases of the pattern, one hour apart.
func purchases(pattern simulator.Pattern, n int) []labels.SettledPurchase {
	out := make([]labels.SettledPurchase, n)
	for i := range out {
		out[i] = labels.SettledPurchase{
			TransactionID: fmt.Sprintf("%s-%d", pattern, i),
			EventTime:     start.Add(time.Duration(i) * time.Hour),
			AmountMinor:   1000 + int64(i),
			Pattern:       pattern,
		}
	}
	return out
}

func TestPlanIsDeterministicForASeed(t *testing.T) {
	all := append(purchases(simulator.PatternStolenCard, 300), purchases(simulator.PatternNormal, 3000)...)

	first, again := mockbank.Plan(all, 7), mockbank.Plan(all, 7)
	other := mockbank.Plan(all, 8)

	if len(first) == 0 || !reflect.DeepEqual(first, again) {
		t.Fatalf("two plans with seed 7 differ or are empty: %d and %d chargebacks", len(first), len(again))
	}
	if reflect.DeepEqual(first, other) {
		t.Error("seeds 7 and 8 planned the same chargebacks")
	}
}

func TestPlanGivesAPurchaseTheSameChargebackWhateverElseIsPlanned(t *testing.T) {
	all := purchases(simulator.PatternStolenCard, 200)
	full := mockbank.Plan(all, 3)

	half := mockbank.Plan(all[100:], 3)

	var fullSecondHalf []models.Chargeback
	for _, c := range full {
		if txIndex(c.TransactionID) >= 100 {
			fullSecondHalf = append(fullSecondHalf, c)
		}
	}
	if !reflect.DeepEqual(half, fullSecondHalf) {
		t.Errorf("planning half of the purchases gave %d chargebacks, want the same %d as in the full plan", len(half), len(fullSecondHalf))
	}
}

func txIndex(transactionID string) int {
	var i int
	fmt.Sscanf(transactionID[strings.LastIndex(transactionID, "-")+1:], "%d", &i)
	return i
}

func TestPlanChargesBackFraudOftenAndLegitimatePurchasesRarely(t *testing.T) {
	tests := []struct {
		pattern  simulator.Pattern
		min, max float64
	}{
		{simulator.PatternStolenCard, 0.67, 0.73},
		{simulator.PatternAccountTakeover, 0.27, 0.33},
		{simulator.PatternCardTesting, 0.02, 0.04},
		{simulator.PatternNormal, 0.002, 0.006},
	}
	for _, tt := range tests {
		t.Run(string(tt.pattern), func(t *testing.T) {
			all := purchases(tt.pattern, 20000)

			planned := mockbank.Plan(all, 1)

			share := float64(len(planned)) / float64(len(all))
			if share < tt.min || share > tt.max {
				t.Errorf("charged back %.4f of %s purchases, want %.3f to %.3f", share, tt.pattern, tt.min, tt.max)
			}
		})
	}
}

func TestPlanUsesRealisticReasonsAndDelays(t *testing.T) {
	all := purchases(simulator.PatternStolenCard, 2000)
	byID := map[string]labels.SettledPurchase{}
	for _, p := range all {
		byID[p.TransactionID] = p
	}

	for _, c := range mockbank.Plan(all, 5) {
		p := byID[c.TransactionID]
		delay := c.ReceivedAt.Sub(p.EventTime)
		if delay < 47*time.Hour || delay > 120*24*time.Hour {
			t.Fatalf("%s received %s after the purchase, want 2 to 120 days", c.ChargebackID, delay)
		}
		if c.Reason != models.ChargebackFraudCardNotPresent || c.AmountMinor != p.AmountMinor {
			t.Fatalf("stolen card chargeback %+v, want fraud_card_not_present for the full amount", c)
		}
	}
}

func TestArrivedByKeepsOnlyChargebacksReceivedByTheTime(t *testing.T) {
	asOf := start.AddDate(0, 0, 30)
	planned := []models.Chargeback{
		{ChargebackID: "early", ReceivedAt: asOf.Add(-time.Second)},
		{ChargebackID: "exactly", ReceivedAt: asOf},
		{ChargebackID: "late", ReceivedAt: asOf.Add(time.Second)},
	}

	arrived := mockbank.ArrivedBy(planned, asOf)

	if len(arrived) != 2 || arrived[0].ChargebackID != "early" || arrived[1].ChargebackID != "exactly" {
		t.Errorf("arrived = %+v, want early and exactly", arrived)
	}
}

// fakeWebhook verifies each delivery's signature and records it, answering
// 202 the first time and 200 for a repeat.
type fakeWebhook struct {
	mu       sync.Mutex
	received map[string]string
}

func (f *fakeWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.URL.Path != "/chargebacks" || platform.VerifyWebhook(secret, r.Header, body, time.Now()) != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var c struct {
		ChargebackID string `json:"chargeback_id"`
	}
	if err := json.Unmarshal(body, &c); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, seen := f.received[c.ChargebackID]; seen {
		w.WriteHeader(http.StatusOK)
		return
	}
	f.received[c.ChargebackID] = string(body)
	w.WriteHeader(http.StatusAccepted)
}

func TestSendDeliversOnlyWhatArrivedByTheAsOfTime(t *testing.T) {
	webhook := &fakeWebhook{received: map[string]string{}}
	server := httptest.NewServer(webhook)
	defer server.Close()
	sender := &mockbank.Sender{BaseURL: server.URL, Secret: secret, HTTP: server.Client()}
	planned := mockbank.Plan(purchases(simulator.PatternStolenCard, 24*120), 9)
	early, later := start.AddDate(0, 0, 60), start.AddDate(0, 0, 240)

	first, err := sender.Send(context.Background(), mockbank.ArrivedBy(planned, early))
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	for id, body := range webhook.received {
		var c struct {
			ReceivedAt time.Time `json:"received_at"`
		}
		_ = json.Unmarshal([]byte(body), &c)
		if c.ReceivedAt.After(early) {
			t.Fatalf("%s was received at %s, after the as-of time %s", id, c.ReceivedAt, early)
		}
	}
	second, err := sender.Send(context.Background(), mockbank.ArrivedBy(planned, later))

	if err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if first.Recorded == 0 || first.Repeated != 0 || first.Recorded >= len(planned) {
		t.Errorf("first run %+v, want some but not all of the %d planned chargebacks", first, len(planned))
	}
	if second.Repeated != first.Recorded || first.Recorded+second.Recorded != len(planned) {
		t.Errorf("second run %+v after first %+v, want the first ones repeated and the rest of %d recorded", second, first, len(planned))
	}
}

func TestSendStopsWhenTheWebhookRefuses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"conflict"}`))
	}))
	defer server.Close()
	sender := &mockbank.Sender{BaseURL: server.URL, Secret: secret, HTTP: server.Client()}
	planned := []models.Chargeback{{ChargebackID: "cb-1"}, {ChargebackID: "cb-2"}}

	sum, err := sender.Send(context.Background(), planned)

	if err == nil || !strings.Contains(err.Error(), "409") || sum != (mockbank.SendSummary{}) {
		t.Fatalf("Send = %+v, %v; want a 409 error before anything is counted", sum, err)
	}
}
