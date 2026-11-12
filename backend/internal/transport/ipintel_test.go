package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

// countingMock is the mock provider with a count of its lookups.
type countingMock struct {
	*ipintel.Mock
	calls atomic.Int32
}

func (m *countingMock) Lookup(ctx context.Context, addr netip.Addr) (ipintel.Result, error) {
	m.calls.Add(1)
	return m.Mock.Lookup(ctx, addr)
}

func TestIPIntelligenceIsSavedAndShownAsEvidence(t *testing.T) {
	tx := testTx(t)
	rs := scoring.Ruleset{Version: 1, ReviewThreshold: 50, BlockThreshold: 80, Rules: map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleDatacenterIP: {Enabled: true, Weight: 50},
	}}
	handler := newHandler(fakeRulesets{rs: rs}, storage.NewBlocklistStore(tx), storage.NewPurchaseStore(tx))
	provider := &countingMock{Mock: ipintel.NewMock(ipintel.DefaultRanges)}
	handler.service.UseIPIntel(provider)
	mux := http.NewServeMux()
	registerScore(mux, handler, testKey)
	body := withVerification(`, "ip_address": "192.0.2.10"`)

	first := postScore(t.Context(), mux, testKey, body)
	retry := postScore(t.Context(), mux, testKey, body)

	scored := decodeJSON[scoreResponse](t, first)
	if scored.Outcome != scoring.OutcomeReview || len(scored.FiredRules) != 1 || scored.FiredRules[0].Name != scoring.RuleDatacenterIP {
		t.Fatalf("score = %+v, want a review from datacenter_ip", scored)
	}
	if retry.Code != http.StatusOK || retry.Body.String() != first.Body.String() || provider.calls.Load() != 1 {
		t.Fatalf("retry = %d %s after %d lookups, want the saved decision after 1", retry.Code, retry.Body, provider.calls.Load())
	}
	var purchaseID int64
	if err := tx.QueryRow(t.Context(), `SELECT id FROM purchases WHERE transaction_id = 'txn-1'`).Scan(&purchaseID); err != nil {
		t.Fatal(err)
	}
	evidence := requestReviewRead(t, storage.NewReviewStore(tx), "/reviews/"+strconv.FormatInt(purchaseID, 10), queueTestKey)
	got := decodeJSON[caseResponse](t, evidence)
	want := &ipIntelResponse{Provider: "mock", Available: true, Hosting: true}
	if !reflect.DeepEqual(got.IPIntel, want) {
		t.Errorf("case ip_intel = %+v, want %+v", got.IPIntel, want)
	}
}

func TestCaseShowsNullIPIntelligenceWithoutALookup(t *testing.T) {
	store := caseFunc(func(_ context.Context, id int64) (models.CaseItem, error) {
		return models.CaseItem{QueueItem: models.QueueItem{PurchaseID: id}, RulesetVersion: 1}, nil
	})

	w := requestCase(t, store, "/reviews/7", queueTestKey)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status = %d, err = %v, body = %s", w.Code, err, w.Body)
	}
	if got, present := body["ip_intel"]; !present || got != nil {
		t.Errorf("ip_intel = %v (present %v), want null", got, present)
	}
}
