package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/review"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

func TestScoreTrustedHistoryLookup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enabled    bool
		body       string
		blocked    bool
		wantStatus int
		wantCalls  int
	}{
		{"disabled", false, blocklistBody, false, http.StatusOK, 0},
		{"missing device", true, minimalBody, false, http.StatusOK, 0},
		{"below floor", true, strings.Replace(blocklistBody, "1999", "999", 1), false, http.StatusOK, 0},
		{"blocklist match", true, blocklistBody, true, http.StatusOK, 0},
		{"history failure", true, blocklistBody, false, http.StatusInternalServerError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := scoring.Ruleset{Version: 7, ReviewThreshold: 50, BlockThreshold: 80, Rules: map[scoring.RuleName]scoring.RuleConfig{
				scoring.RuleNewDeviceSpike: {Enabled: tc.enabled, Limit: 3, WindowDays: 90, MinAmountMinor: 1000, AmountMultiplier: 3},
			}}
			store := &fakePurchases{trustedHistoryErr: errors.New("trusted history unavailable")}
			bl := &fakeBlocklist{matched: tc.blocked, entry: models.BlocklistEntry{Type: models.BlocklistCard}}
			rec := serveWith(t, fakeRulesets{rs: rs}, bl, store, testKey, tc.body)
			if rec.Code != tc.wantStatus || store.trustedHistoryCalls != tc.wantCalls {
				t.Errorf("status = %d, history calls = %d, want %d and %d: %s",
					rec.Code, store.trustedHistoryCalls, tc.wantStatus, tc.wantCalls, rec.Body)
			}
			if tc.wantStatus == http.StatusInternalServerError && len(store.saved) != 0 {
				t.Error("purchase saved after a trusted history lookup failure")
			}
		})
	}
}

func TestTrustedHistoryWithPostgres(t *testing.T) {
	tx := testTx(t)
	ctx := t.Context()
	store := storage.NewPurchaseStore(tx)
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	candidate := minimalPurchase("history-m")
	candidate.EventTime = at
	candidate.DeviceID = ptr("attacker")
	const window = 90 * 24 * time.Hour
	for _, row := range []struct {
		id, device string
		offset     time.Duration
		amount     int64
		outcome    scoring.Outcome
		resolution string
	}{
		{"history-allow", "known", -time.Hour, 300, scoring.OutcomeAllow, ""},
		{"history-just-inside", "", -window + time.Microsecond, 350, scoring.OutcomeAllow, ""},
		{"history-a", "known", 0, 450, scoring.OutcomeReview, "approve"},
		{"history-at-cutoff", "attacker", -window, 90000, scoring.OutcomeAllow, ""},
		{"history-future", "attacker", time.Microsecond, 90000, scoring.OutcomeAllow, ""},
		{"history-z", "attacker", 0, 90000, scoring.OutcomeAllow, ""},
		{"history-m", "attacker", 0, 90000, scoring.OutcomeAllow, ""},
		{"history-blocked", "attacker", -time.Minute, 90000, scoring.OutcomeBlock, ""},
		{"history-rejected", "attacker", -time.Minute, 90000, scoring.OutcomeReview, "reject"},
		{"history-unresolved", "attacker", -time.Minute, 90000, scoring.OutcomeReview, ""},
		{"other-player", "attacker", -time.Minute, 90000, scoring.OutcomeAllow, ""},
	} {
		p := minimalPurchase(row.id)
		p.EventTime, p.AmountMinor = at.Add(row.offset), row.amount
		if row.device != "" {
			p.DeviceID = ptr(row.device)
		}
		if row.id == "other-player" {
			p.PlayerID = "other-player"
		}
		id, err := store.Save(ctx, p, models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: row.outcome}})
		if err != nil {
			t.Fatal(err)
		}
		if row.resolution != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO review_resolutions (purchase_id, decision, analyst_id) VALUES ($1, $2, 'test')`, id, row.resolution); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		device string
		days   int32
		count  int64
		total  string
		seen   bool
	}{
		{"attacker", 90, 3, "1100", false},
		{"known", 90, 3, "1100", true},
		{"attacker", 1, 2, "750", false},
	} {
		candidate.DeviceID = ptr(tc.device)
		got, err := store.TrustedHistory(ctx, candidate, tc.days)
		if err != nil || got.Count != tc.count || got.TotalMinor.String() != tc.total || got.DeviceSeen != tc.seen {
			t.Errorf("trusted history for %+v = %+v, %v", tc, got, err)
		}
	}
}

type trustedHistoryStore struct {
	*storage.PurchaseStore
	calls int
}

func (s *trustedHistoryStore) TrustedHistory(ctx context.Context, p models.Purchase, windowDays int32) (*scoring.TrustedPurchaseHistory, error) {
	s.calls++
	return s.PurchaseStore.TrustedHistory(ctx, p, windowDays)
}

// spikeServer serves the ruleset, review and score routes from one
// transaction, and counts the trusted history lookups.
type spikeServer struct {
	tx        pgx.Tx
	mux       *http.ServeMux
	rules     *storage.RulesetStore
	purchases *trustedHistoryStore
}

func newSpikeServer(t *testing.T) spikeServer {
	t.Helper()
	tx := testTx(t)
	srv := spikeServer{tx: tx, mux: http.NewServeMux(), rules: storage.NewRulesetStore(tx), purchases: &trustedHistoryStore{PurchaseStore: storage.NewPurchaseStore(tx)}}
	keys, err := platform.ParseAnalystKeys(`[{"analyst_id":"test","key":"review-test-key"}]`, testKey)
	if err != nil {
		t.Fatal(err)
	}
	RegisterRulesetRoutes(srv.mux, NewRulesetHandler(ruleset.NewService(srv.rules)), platform.Access{ServiceKey: testKey, Operators: testOperators(t)})
	RegisterReviewRoutes(srv.mux, NewReviewHandler(review.NewService(storage.NewReviewStore(tx)), false), platform.Access{Analysts: keys})
	registerScore(srv.mux, newHandler(srv.rules, storage.NewBlocklistStore(tx), srv.purchases), testKey)
	return srv
}

func (srv spikeServer) request(ctx context.Context, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
	req.Header.Set(platform.APIKeyHeader, key)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	return rec
}

// saveTrustedPurchases saves two allowed purchases and one under review on
// the device "trusted", an hour before at. It returns the ID of the purchase
// under review.
func (srv spikeServer) saveTrustedPurchases(t *testing.T, rulesetVersion int, at time.Time) int64 {
	t.Helper()
	var reviewID int64
	for i, outcome := range []scoring.Outcome{scoring.OutcomeAllow, scoring.OutcomeAllow, scoring.OutcomeReview} {
		p := minimalPurchase(fmt.Sprintf("spike-trusted-%d", i))
		p.EventTime, p.DeviceID, p.AmountMinor = at.Add(-time.Hour), ptr("trusted"), 400
		var err error
		reviewID, err = srv.purchases.Save(t.Context(), p, models.Decision{RulesetVersion: rulesetVersion, Result: scoring.Result{Outcome: outcome}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return reviewID
}

// publishSpikeRuleset publishes a ruleset with only the new device spike rule
// and checks that it became the active one and left the previous version's
// rules as they were. It returns the new version.
func (srv spikeServer) publishSpikeRuleset(t *testing.T, previous scoring.Ruleset) int {
	t.Helper()
	ctx := t.Context()
	published := srv.request(ctx, http.MethodPost, "/rulesets", `{
		"review_threshold":20,"block_threshold":40,"rules":{"new_device_spike":{
			"enabled":true,"weight":50,"limit":3,"window_days":30,"min_amount_minor":1500,"amount_multiplier":4}}
	}`, operatorTestKey)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", published.Code, published.Body)
	}
	active := srv.request(ctx, http.MethodGet, "/rulesets/active", "", testKey)
	var loaded struct {
		Version int
		Rules   map[string]map[string]json.RawMessage
	}
	if active.Code != http.StatusOK || active.Body.String() != published.Body.String() ||
		json.Unmarshal(active.Body.Bytes(), &loaded) != nil || loaded.Version <= previous.Version {
		t.Fatalf("active ruleset did not round-trip: %d %s", active.Code, active.Body)
	}
	for field, value := range map[string]string{"enabled": "true", "weight": "50", "limit": "3", "window_days": "30", "min_amount_minor": "1500", "amount_multiplier": "4"} {
		if got := string(loaded.Rules["new_device_spike"][field]); got != value {
			t.Errorf("active %s = %s, want %s", field, got, value)
		}
	}
	if n := countRows(t, srv.tx, `SELECT count(*) FROM ruleset_rules WHERE ruleset_version = $1
		AND window_days IS NULL AND min_amount_minor IS NULL AND amount_multiplier IS NULL`, previous.Version); n != len(previous.Rules) {
		t.Errorf("prior ruleset rules without spike settings = %d, want %d", n, len(previous.Rules))
	}
	return loaded.Version
}

func TestScoreNewDeviceSpikeWithPostgres(t *testing.T) {
	srv := newSpikeServer(t)
	ctx := t.Context()
	mux, purchaseStore := srv.mux, srv.purchases
	originalRuleset, err := srv.rules.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	reviewID := srv.saveTrustedPurchases(t, originalRuleset.Version, at)

	body := func(id, device string, amount int64, when time.Time) string {
		return fmt.Sprintf(`{"transaction_id":%q,"card_token":"spike-card","device_id":%q,
			"player_id":"player-1","amount_minor":%d,"event_time":%q}`, id, device, amount, when.Format(time.RFC3339))
	}
	originalBody := body("spike-retry", "retry-device", 1600, at.Add(10*time.Minute))
	first := postScore(ctx, mux, testKey, originalBody)
	original := decodeJSON[scoreResponse](t, first)
	if first.Code != http.StatusOK || original.Outcome != scoring.OutcomeAllow || original.RulesetVersion != originalRuleset.Version || purchaseStore.calls != 0 {
		t.Fatalf("score before activation: %d %s, history calls = %d", first.Code, first.Body, purchaseStore.calls)
	}

	spikeVersion := srv.publishSpikeRuleset(t, originalRuleset)

	approved := srv.request(ctx, http.MethodPost, fmt.Sprintf("/reviews/%d/decision", reviewID), `{"decision":"approve"}`, "review-test-key")
	if approved.Code != http.StatusCreated {
		t.Fatalf("approve prior review: %d %s", approved.Code, approved.Body)
	}
	for i, amount := range []int64{90000, 1600} {
		id := fmt.Sprintf("spike-attempt-%d", i)
		rec := postScore(ctx, mux, testKey, body(id, "attacker", amount, at.Add(time.Duration(i)*time.Minute)))
		got := decodeJSON[scoreResponse](t, rec)
		if rec.Code != http.StatusOK || got.Outcome != scoring.OutcomeBlock || got.RulesetVersion != spikeVersion ||
			got.Score != 50 || len(got.FiredRules) != 1 || got.FiredRules[0].Name != scoring.RuleNewDeviceSpike ||
			got.FiredRules[0].Points != 50 || !strings.Contains(got.FiredRules[0].Message, "4 times the average of 3 trusted purchases in 30 days") {
			t.Fatalf("attempt %d: %d %s, want block with new device spike evidence", i+1, rec.Code, rec.Body)
		}
		retry := postScore(ctx, mux, testKey, body(id, "attacker", amount, at.Add(time.Duration(i)*time.Minute)))
		if retry.Code != http.StatusOK || retry.Body.String() != rec.Body.String() {
			t.Errorf("blocked retry: %d %s, want saved evidence %s", retry.Code, retry.Body, rec.Body)
		}
	}
	retry := postScore(ctx, mux, testKey, originalBody)
	if retry.Code != http.StatusOK || retry.Body.String() != first.Body.String() {
		t.Errorf("pre-activation retry: %d %s, want original %s", retry.Code, retry.Body, first.Body)
	}
	conflict := postScore(ctx, mux, testKey, body("spike-retry", "retry-device", 1601, at.Add(10*time.Minute)))
	if conflict.Code != http.StatusConflict || purchaseStore.calls != 2 {
		t.Errorf("conflicting retry: %d %s, history calls = %d, want 409 and 2 calls", conflict.Code, conflict.Body, purchaseStore.calls)
	}
}
