package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

const testKey = "test-key"
const operatorTestKey = "operator-key"

func testOperators(t *testing.T) platform.OperatorKeys {
	t.Helper()
	keys, err := platform.ParseOperatorKeys(`[{"operator_id":"test-operator","key":"`+operatorTestKey+`"}]`, testKey, platform.AnalystKeys{})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

var testRuleset = scoring.Ruleset{
	Version:         7,
	ReviewThreshold: 50,
	BlockThreshold:  80,
	Rules: map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleHighAmount:      {Enabled: true, Weight: 30, Limit: 50000},
		scoring.RuleCardVelocity:    {Enabled: true, Weight: 40, Limit: 5},
		scoring.RuleNewAccount:      {Enabled: true, Weight: 20, Limit: 24},
		scoring.RuleCountryMismatch: {Enabled: true, Weight: 25},
	},
	Model: scoring.ModelSettings{Mode: scoring.ModelAssist},
}

type fakeRulesets struct {
	rs  scoring.Ruleset
	err error
}

func (f fakeRulesets) Active(context.Context) (scoring.Ruleset, error) { return f.rs, f.err }

type fakePurchases struct {
	find             func(models.Purchase) (*models.Decision, error)
	findCalls        int
	cardUses         int
	countErr         error
	deviceCardsErr   error
	deviceCardsCalls int
	historyErr       error
	earlierSum       float64
	earlierCount     int64
	earlierErr       error
	history          features.History
	saveErr          error
	countCard        string
	countAt          time.Time
	saved            []models.Purchase
	decisions        []models.Decision

	trustedHistoryErr   error
	trustedHistoryCalls int
}

func (f *fakePurchases) FindSavedDecision(_ context.Context, p models.Purchase) (*models.Decision, error) {
	f.findCalls++
	if f.find != nil {
		return f.find(p)
	}
	return nil, nil
}

func (f *fakePurchases) CardPurchasesLastHour(_ context.Context, card string, at time.Time) (int, error) {
	f.countCard, f.countAt = card, at
	return f.cardUses, f.countErr
}

func (f *fakePurchases) DeviceCardsLastHour(context.Context, string, string, time.Time) (int64, error) {
	f.deviceCardsCalls++
	return 0, f.deviceCardsErr
}

func (f *fakePurchases) TrustedHistory(context.Context, models.Purchase, int32) (*scoring.TrustedPurchaseHistory, error) {
	f.trustedHistoryCalls++
	return nil, f.trustedHistoryErr
}

func (f *fakePurchases) FeatureHistory(context.Context, features.Input) (features.History, error) {
	return f.history, f.historyErr
}

func (f *fakePurchases) PlayerEarlierModelScores(context.Context, string, time.Time, string) (float64, int64, error) {
	return f.earlierSum, f.earlierCount, f.earlierErr
}

func (f *fakePurchases) Save(_ context.Context, p models.Purchase, d models.Decision) (int64, error) {
	if f.saveErr != nil {
		return 0, f.saveErr
	}
	f.saved = append(f.saved, p)
	f.decisions = append(f.decisions, d)
	return int64(len(f.saved)), nil
}

// fakeBlocklist matches every purchase when matched is set, and records the
// subject it was asked about.
type fakeBlocklist struct {
	entry   models.BlocklistEntry
	matched bool
	err     error
	subject models.BlocklistSubject
}

func (f *fakeBlocklist) Match(_ context.Context, s models.BlocklistSubject) (models.BlocklistEntry, bool, error) {
	f.subject = s
	return f.entry, f.matched, f.err
}

// serve sends a request to a handler whose blocklist is empty.
func serve(t *testing.T, r score.Rulesets, p score.Purchases, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveWith(t, r, &fakeBlocklist{}, p, key, body)
}

func serveWith(t *testing.T, r score.Rulesets, b score.Blocklist, p score.Purchases, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerScore(mux, newHandler(r, b, p), testKey)
	return postScore(t.Context(), mux, key, body)
}

func postScore(ctx context.Context, mux http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/score", strings.NewReader(body))
	if key != "" {
		req.Header.Set(platform.APIKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

const minimalBody = `{
	"transaction_id": "txn-1",
	"event_time": "2026-11-08T10:00:00Z",
	"amount_minor": 1999,
	"player_id": "player-1",
	"card_token": "card-1"
}`

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response %s: %v", rec.Body.String(), err)
	}
	return v
}

func TestScoreAllowsMinimalPurchase(t *testing.T) {
	purchases := &fakePurchases{}
	rec := serve(t, fakeRulesets{rs: testRuleset}, purchases, testKey, minimalBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"fired_rules":[]`) {
		t.Errorf("body %s, want an empty fired_rules list", rec.Body.String())
	}
	got := decodeJSON[scoreResponse](t, rec)
	want := scoreResponse{
		TransactionID:  "txn-1",
		Outcome:        scoring.OutcomeAllow,
		FiredRules:     []scoring.FiredRule{},
		RulesetVersion: 7,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("response = %+v, want %+v", got, want)
	}

	wantPurchase := models.Purchase{
		TransactionID: "txn-1",
		EventTime:     time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
		AmountMinor:   1999,
		PlayerID:      "player-1",
		CardToken:     "card-1",
	}
	if len(purchases.saved) != 1 || !reflect.DeepEqual(purchases.saved[0], wantPurchase) {
		t.Errorf("saved purchases = %+v, want [%+v]", purchases.saved, wantPurchase)
	}
	d := purchases.decisions[0]
	if d.RulesetVersion != 7 || d.Result.Outcome != scoring.OutcomeAllow || d.ModelScore != nil {
		t.Errorf("saved decision = %+v, want ruleset 7, allow, no model score", d)
	}
	if len(d.ModelInputs) != len(features.Names) || d.ModelInputs["amount"] != 19.99 || d.ModelInputs["player_txn_count"] != 0 {
		t.Errorf("model inputs = %v, want all %d features with amount 19.99 and no history", d.ModelInputs, len(features.Names))
	}
}

func TestScoreReturnsFiredRules(t *testing.T) {
	purchases := &fakePurchases{cardUses: 5}
	body := `{
		"transaction_id": "txn-2",
		"event_time": "2026-11-08T14:00:00+04:00",
		"amount_minor": 60000,
		"player_id": "player-2",
		"card_token": "card-2",
		"card_country": "AZ",
		"ip_country": "TR",
		"ip_address": "203.0.113.9",
		"device_id": "device-2",
		"account_created_at": "2026-11-08T09:00:00Z"
	}`
	rec := serve(t, fakeRulesets{rs: testRuleset}, purchases, testKey, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[scoreResponse](t, rec)
	want := scoreResponse{
		TransactionID: "txn-2",
		Outcome:       scoring.OutcomeBlock,
		Score:         115,
		FiredRules: []scoring.FiredRule{
			{Name: scoring.RuleHighAmount, Points: 30, Message: "Amount 60000 minor units is at or above the limit of 50000"},
			{Name: scoring.RuleCardVelocity, Points: 40, Message: "Card used 5 times in 60 minutes (limit 5)"},
			{Name: scoring.RuleNewAccount, Points: 20, Message: "Account is 1 hour old, under the limit of 24 hours"},
			{Name: scoring.RuleCountryMismatch, Points: 25, Message: "IP country TR differs from card country AZ"},
		},
		RulesetVersion: 7,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("response = %+v, want %+v", got, want)
	}

	utc := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	if purchases.countCard != "card-2" || !purchases.countAt.Equal(utc) {
		t.Errorf("card count asked for (%s, %v), want (card-2, %v)", purchases.countCard, purchases.countAt, utc)
	}
	p := purchases.saved[0]
	if p.EventTime != utc || p.EventTime.Location() != time.UTC {
		t.Errorf("saved event time = %v, want %v in UTC", p.EventTime, utc)
	}
	if p.IPAddress == nil || *p.IPAddress != netip.MustParseAddr("203.0.113.9") {
		t.Errorf("saved IP address = %v, want 203.0.113.9", p.IPAddress)
	}
	if p.DeviceID == nil || *p.DeviceID != "device-2" {
		t.Errorf("saved device ID = %v, want device-2", p.DeviceID)
	}
}

func TestScoreCountryMismatchNeedsCardCountry(t *testing.T) {
	body := strings.Replace(minimalBody, `"card_token": "card-1"`, `"card_token": "card-1", "ip_country": "TR"`, 1)
	rec := serve(t, fakeRulesets{rs: testRuleset}, &fakePurchases{}, testKey, body)

	got := decodeJSON[scoreResponse](t, rec)
	if len(got.FiredRules) != 0 {
		t.Errorf("fired rules = %+v, want none when card_country is omitted", got.FiredRules)
	}
}

func TestScoreRejectsInvalidRequests(t *testing.T) {
	with := func(replacement string) string {
		return strings.Replace(minimalBody, `"amount_minor": 1999`, replacement, 1)
	}

	tests := []struct {
		name      string
		body      string
		wantField string
		wantError string
	}{
		{"negative amount", with(`"amount_minor": -100`), "amount_minor", ""},
		{"zero amount", with(`"amount_minor": 0`), "amount_minor", ""},
		{"missing amount", strings.Replace(minimalBody, `"amount_minor": 1999,`, ``, 1), "amount_minor", ""},
		{"fractional amount", with(`"amount_minor": 19.99`), "amount_minor", ""},
		{"amount as string", with(`"amount_minor": "1999"`), "amount_minor", ""},
		{"amount in exponent form", with(`"amount_minor": 1e3`), "amount_minor", ""},
		{"unknown field", with(`"amount_minor": 1999, "amount": 19.99`), "amount", ""},
		{"empty transaction ID", strings.Replace(minimalBody, `"txn-1"`, `"  "`, 1), "transaction_id", ""},
		{"missing player", strings.Replace(minimalBody, `"player_id": "player-1",`, ``, 1), "player_id", ""},
		{"missing event time", strings.Replace(minimalBody, `"event_time": "2026-11-08T10:00:00Z",`, ``, 1), "event_time", ""},
		{"event time without zone", strings.Replace(minimalBody, `2026-11-08T10:00:00Z`, `2026-11-08 10:00:00`, 1), "", "RFC 3339"},
		{"event time as number", strings.Replace(minimalBody, `"2026-11-08T10:00:00Z"`, `1762596000`, 1), "", "RFC 3339"},
		{"invalid IP address", with(`"amount_minor": 1999, "ip_address": "300.1.1.1"`), "ip_address", ""},
		{"negative distance", with(`"amount_minor": 1999, "billing_distance": -1`), "billing_distance", ""},
		{"account created after event", with(`"amount_minor": 1999, "account_created_at": "2026-11-08T11:00:00Z"`), "account_created_at", ""},
		{"event time far in the future", strings.Replace(minimalBody, `2026-11-08T10:00:00Z`, `2099-01-01T00:00:00Z`, 1), "event_time", ""},
		{"malformed JSON", `{"transaction_id": `, "", "not valid JSON"},
		{"empty body", ``, "", "empty"},
		{"array body", `[]`, "", "JSON object"},
		{"two objects", minimalBody + minimalBody, "", "single JSON object"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			purchases := &fakePurchases{}
			rec := serve(t, fakeRulesets{rs: testRuleset}, purchases, testKey, tt.body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
			got := decodeJSON[platform.ErrorResponse](t, rec)
			if tt.wantField != "" && got.Fields[tt.wantField] == "" {
				t.Errorf("response %+v does not name field %s", got, tt.wantField)
			}
			if !strings.Contains(got.Error, tt.wantError) {
				t.Errorf("error %q does not contain %q", got.Error, tt.wantError)
			}
			if len(purchases.saved) != 0 {
				t.Error("an invalid purchase was saved")
			}
		})
	}
}

func TestEventTimeMayBeAtMostFiveMinutesAhead(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		at    time.Time
		valid bool
	}{
		{"exactly at the limit", now.Add(maxClockSkew), true},
		{"just past the limit", now.Add(maxClockSkew + time.Nanosecond), false},
		{"now", now, true},
		{"years in the past, as in a replay", time.Date(2017, 12, 2, 0, 0, 0, 0, time.UTC), true},
		{"limit in another time zone", now.Add(maxClockSkew).In(time.FixedZone("UTC+4", 4*3600)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := scoreRequest{TransactionID: "txn-1", EventTime: tt.at, AmountMinor: 1999, PlayerID: "player-1", CardToken: "card-1"}
			_, fields := req.purchase(now)
			if got := fields["event_time"] == ""; got != tt.valid {
				t.Errorf("valid = %v, want %v; fields %v", got, tt.valid, fields)
			}
		})
	}
}

func TestScoreRejectsFutureEventTimeWithMessage(t *testing.T) {
	body := strings.Replace(minimalBody, `2026-11-08T10:00:00Z`, `2099-01-01T00:00:00Z`, 1)
	rec := serve(t, fakeRulesets{rs: testRuleset}, &fakePurchases{}, testKey, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	got := decodeJSON[platform.ErrorResponse](t, rec)
	if want := "must not be more than 5 minutes in the future"; got.Fields["event_time"] != want {
		t.Errorf("event_time = %q, want %q", got.Fields["event_time"], want)
	}
}

func TestScoreReportsEveryInvalidField(t *testing.T) {
	body := `{"transaction_id": "", "event_time": "2026-11-08T10:00:00Z", "amount_minor": -1, "player_id": "p", "card_token": ""}`
	purchases := &fakePurchases{}
	rec := serve(t, fakeRulesets{rs: testRuleset}, purchases, testKey, body)
	if rec.Code != http.StatusBadRequest || purchases.findCalls != 0 {
		t.Fatalf("status = %d, lookup calls = %d, want 400 with no lookup", rec.Code, purchases.findCalls)
	}

	got := decodeJSON[platform.ErrorResponse](t, rec)
	for _, f := range []string{"transaction_id", "amount_minor", "card_token"} {
		if got.Fields[f] == "" {
			t.Errorf("fields %v do not include %s", got.Fields, f)
		}
	}
}

func TestScoreRejectsLargeBody(t *testing.T) {
	body := with64KBPadding(minimalBody)
	rec := serve(t, fakeRulesets{rs: testRuleset}, &fakePurchases{}, testKey, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

func with64KBPadding(body string) string {
	return strings.Replace(body, `"card-1"`, fmt.Sprintf("%q", strings.Repeat("x", maxBodyBytes)), 1)
}

func TestScoreRequiresAPIKey(t *testing.T) {
	for _, key := range []string{"", "wrong-key"} {
		purchases := &fakePurchases{}
		rec := serve(t, fakeRulesets{rs: testRuleset}, purchases, key, minimalBody)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("key %q: status = %d, want 401", key, rec.Code)
		}
		if purchases.findCalls != 0 || len(purchases.saved) != 0 {
			t.Errorf("key %q: purchase store was accessed", key)
		}
	}
}

func TestScoreSavedDecision(t *testing.T) {
	saved := &models.Decision{
		RulesetVersion: 2,
		Result: scoring.Result{
			Outcome: scoring.OutcomeReview,
			Score:   60,
			FiredRules: []scoring.FiredRule{
				{Name: scoring.RuleCardVelocity, Points: 60, Message: "Saved rule evidence"},
			},
		},
		ModelScore:   ptr(0.75),
		ModelVersion: ptr("saved-model"),
	}
	boom := errors.New("lookup unavailable")
	tests := []struct {
		name       string
		race       bool
		decision   *models.Decision
		lookupErr  error
		wantStatus int
	}{
		{"matching", false, saved, nil, http.StatusOK},
		{"conflict", false, nil, models.ErrDuplicateTransaction, http.StatusConflict},
		{"lookup error", false, nil, boom, http.StatusInternalServerError},
		{"race matching", true, saved, nil, http.StatusOK},
		{"race conflict", true, nil, models.ErrDuplicateTransaction, http.StatusConflict},
		{"race missing", true, nil, nil, http.StatusInternalServerError},
		{"race lookup error", true, nil, boom, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			purchases := &fakePurchases{}
			active := fakeRulesets{err: errors.New("scoring should be bypassed")}
			blocklist := &fakeBlocklist{err: active.err}
			wantLookups := 1
			if tt.race {
				purchases.saveErr = models.ErrDuplicateTransaction
				active = fakeRulesets{rs: testRuleset}
				blocklist.err = nil
				wantLookups = 2
			}
			purchases.find = func(models.Purchase) (*models.Decision, error) {
				if tt.race && purchases.findCalls == 1 {
					return nil, nil
				}
				return tt.decision, tt.lookupErr
			}
			rec := serveWith(t, active, blocklist, purchases, testKey, minimalBody)
			if rec.Code != tt.wantStatus || purchases.findCalls != wantLookups || len(purchases.saved) != 0 {
				t.Fatalf("status = %d, lookups = %d, saved = %d, body = %s", rec.Code, purchases.findCalls, len(purchases.saved), rec.Body)
			}
			switch tt.wantStatus {
			case http.StatusOK:
				got := decodeJSON[scoreResponse](t, rec)
				want := scoreResponse{
					TransactionID: "txn-1", Outcome: saved.Result.Outcome, Score: saved.Result.Score,
					FiredRules: saved.Result.FiredRules, RulesetVersion: saved.RulesetVersion,
					ModelScore: saved.ModelScore, ModelVersion: saved.ModelVersion,
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("response = %+v, want %+v", got, want)
				}
			case http.StatusConflict:
				if got := decodeJSON[platform.ErrorResponse](t, rec); got.Fields["transaction_id"] != "is already scored" {
					t.Errorf("unexpected conflict body %+v", got)
				}
			case http.StatusInternalServerError:
				if got := decodeJSON[platform.ErrorResponse](t, rec); got.Error != "internal error" {
					t.Errorf("unexpected error body %+v", got)
				}
			}
		})
	}
}

func TestScoreInternalErrors(t *testing.T) {
	boom := errors.New("database is down")
	tests := []struct {
		name      string
		rulesets  fakeRulesets
		purchases *fakePurchases
	}{
		{"ruleset", fakeRulesets{err: boom}, &fakePurchases{}},
		{"history", fakeRulesets{rs: testRuleset}, &fakePurchases{countErr: boom}},
		{"feature history", fakeRulesets{rs: testRuleset}, &fakePurchases{historyErr: boom}},
		{"save", fakeRulesets{rs: testRuleset}, &fakePurchases{saveErr: boom}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, tt.rulesets, tt.purchases, testKey, minimalBody)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "database is down") {
				t.Errorf("response %s exposes the internal error", rec.Body.String())
			}
		})
	}
}

const blocklistBody = `{
	"transaction_id": "txn-bl",
	"event_time": "2026-11-08T10:00:00Z",
	"amount_minor": 1999,
	"player_id": "player-9",
	"card_token": "card-9",
	"device_id": "device-9",
	"ip_address": "203.0.113.9"
}`

func wantBlocklistSubject() models.BlocklistSubject {
	addr := netip.MustParseAddr("203.0.113.9")
	return models.BlocklistSubject{CardToken: "card-9", PlayerID: "player-9", DeviceID: ptr("device-9"), IPAddress: &addr}
}

func TestScoreBlocksBlocklistMatch(t *testing.T) {
	purchases := &fakePurchases{}
	bl := &fakeBlocklist{entry: models.BlocklistEntry{Type: models.BlocklistCard, Value: "card-9", Reason: "chargebacks"}, matched: true}
	rec := serveWith(t, fakeRulesets{rs: testRuleset}, bl, purchases, testKey, blocklistBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[scoreResponse](t, rec)
	want := scoreResponse{
		TransactionID: "txn-bl",
		Outcome:       scoring.OutcomeBlock,
		FiredRules: []scoring.FiredRule{
			{Name: scoring.RuleBlocklist, Message: "Blocked: the card is on the blocklist"},
		},
		RulesetVersion: 7,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("response = %+v, want %+v", got, want)
	}
	if want := wantBlocklistSubject(); !reflect.DeepEqual(bl.subject, want) {
		t.Errorf("blocklist asked about %+v, want %+v", bl.subject, want)
	}
	if purchases.countCard != "" {
		t.Errorf("the rules ran: card history was read for %q", purchases.countCard)
	}
	if len(purchases.decisions) != 1 {
		t.Fatalf("saved %d decisions, want 1", len(purchases.decisions))
	}
	if d := purchases.decisions[0]; d.RulesetVersion != 7 || d.Result.Outcome != scoring.OutcomeBlock || d.Result.Score != 0 {
		t.Errorf("saved decision = %+v, want ruleset 7, block, score 0", d)
	}
}

func TestScoreContinuesToRulesOnBlocklistMiss(t *testing.T) {
	purchases := &fakePurchases{}
	bl := &fakeBlocklist{}
	rec := serveWith(t, fakeRulesets{rs: testRuleset}, bl, purchases, testKey, blocklistBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if got := decodeJSON[scoreResponse](t, rec); got.Outcome != scoring.OutcomeAllow {
		t.Errorf("outcome = %s, want allow", got.Outcome)
	}
	if want := wantBlocklistSubject(); !reflect.DeepEqual(bl.subject, want) {
		t.Errorf("blocklist asked about %+v, want %+v", bl.subject, want)
	}
	if purchases.countCard != "card-9" {
		t.Errorf("card history was read for %q, want card-9", purchases.countCard)
	}
}

func TestScoreDeviceHistoryLookup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rule       *scoring.RuleConfig
		body       string
		blocked    bool
		wantCalls  int
		wantStatus int
	}{
		{"omitted rule", nil, blocklistBody, false, 0, http.StatusOK},
		{"disabled rule", &scoring.RuleConfig{Limit: 5}, blocklistBody, false, 0, http.StatusOK},
		{"missing device", &scoring.RuleConfig{Enabled: true, Limit: 5}, minimalBody, false, 0, http.StatusOK},
		{"blocklist match", &scoring.RuleConfig{Enabled: true, Limit: 5}, blocklistBody, true, 0, http.StatusOK},
		{"lookup error", &scoring.RuleConfig{Enabled: true, Limit: 5}, blocklistBody, false, 1, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := scoring.Ruleset{Version: 7, ReviewThreshold: 50, BlockThreshold: 80}
			if tc.rule != nil {
				rs.Rules = map[scoring.RuleName]scoring.RuleConfig{scoring.RuleDeviceCardCount: *tc.rule}
			}
			store := &fakePurchases{deviceCardsErr: errors.New("device history unavailable")}
			bl := &fakeBlocklist{matched: tc.blocked, entry: models.BlocklistEntry{Type: models.BlocklistCard}}
			rec := serveWith(t, fakeRulesets{rs: rs}, bl, store, testKey, tc.body)
			if rec.Code != tc.wantStatus || store.deviceCardsCalls != tc.wantCalls {
				t.Errorf("status = %d, device history calls = %d, want %d and %d: %s",
					rec.Code, store.deviceCardsCalls, tc.wantStatus, tc.wantCalls, rec.Body)
			}
			if tc.wantStatus == http.StatusInternalServerError && len(store.saved) != 0 {
				t.Error("purchase saved after a device history lookup failure")
			}
		})
	}
}

func TestScoreBlocklistError(t *testing.T) {
	purchases := &fakePurchases{}
	bl := &fakeBlocklist{err: errors.New("database is down")}
	rec := serveWith(t, fakeRulesets{rs: testRuleset}, bl, purchases, testKey, minimalBody)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is down") {
		t.Errorf("response %s exposes the internal error", rec.Body.String())
	}
	if len(purchases.saved) != 0 {
		t.Error("a purchase was saved although the blocklist check failed")
	}
}

// TestScoreBlocklistWithPostgres blocks a purchase whose player is on the
// blocklist and checks the saved decision.
func TestScoreBlocklistWithPostgres(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	if _, err := tx.Exec(ctx, `INSERT INTO blocklist_entries (entry_type, value, reason) VALUES ('player', 'bl-player', 'fraud ring')`); err != nil {
		t.Fatalf("insert blocklist entry: %v", err)
	}

	mux := http.NewServeMux()
	registerScore(mux, newHandler(storage.NewRulesetStore(tx), storage.NewBlocklistStore(tx), storage.NewPurchaseStore(tx)), testKey)
	req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(`{"transaction_id": "bl-1",
		"event_time": "2026-11-08T10:00:00Z", "amount_minor": 1000, "player_id": "bl-player", "card_token": "bl-card"}`))
	req.Header.Set(platform.APIKeyHeader, testKey)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if got := decodeJSON[scoreResponse](t, rec); rec.Code != http.StatusOK || got.Outcome != scoring.OutcomeBlock {
		t.Fatalf("status %d, response %s, want 200 with outcome block", rec.Code, rec.Body.String())
	}
	var outcome string
	var score int
	err := tx.QueryRow(ctx, `
		SELECT d.outcome, d.rule_score FROM decisions d JOIN purchases p ON p.id = d.purchase_id
		WHERE p.transaction_id = 'bl-1'`).Scan(&outcome, &score)
	if err != nil || outcome != "block" || score != 0 {
		t.Errorf("saved decision = (%s, %d, %v), want (block, 0)", outcome, score, err)
	}
}

// TestScoreWithPostgres runs the endpoint against the database: the sixth
// purchase on a card within an hour fires card_velocity under the default ruleset.
func TestScoreWithPostgres(t *testing.T) {
	tx := testTx(t)
	h := newHandler(storage.NewRulesetStore(tx), storage.NewBlocklistStore(tx), storage.NewPurchaseStore(tx))
	mux := http.NewServeMux()
	registerScore(mux, h, testKey)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(body))
		req.Header.Set(platform.APIKeyHeader, testKey)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	body := func(i int) string {
		return fmt.Sprintf(`{"transaction_id": "pg-%d", "event_time": "2026-11-08T10:%02d:00Z",
			"amount_minor": 1000, "player_id": "pg-player", "card_token": "pg-card"}`, i, i)
	}

	for i := range 5 {
		rec := post(body(i))
		if got := decodeJSON[scoreResponse](t, rec); rec.Code != http.StatusOK || len(got.FiredRules) != 0 {
			t.Fatalf("purchase %d: status %d, response %s, want 200 with no fired rules", i, rec.Code, rec.Body.String())
		}
	}

	rec := post(body(5))
	got := decodeJSON[scoreResponse](t, rec)
	want := []scoring.FiredRule{{Name: scoring.RuleCardVelocity, Points: 40, Message: "Card used 5 times in 60 minutes (limit 5)"}}
	if rec.Code != http.StatusOK || !reflect.DeepEqual(got.FiredRules, want) || got.RulesetVersion != 1 {
		t.Errorf("sixth purchase: status %d, response %+v, want card_velocity fired under ruleset 1", rec.Code, got)
	}

	if retry := post(body(5)); retry.Code != http.StatusOK || retry.Body.String() != rec.Body.String() {
		t.Errorf("repeated transaction: status %d, body %s, want original response %s", retry.Code, retry.Body, rec.Body)
	}

	var outcome string
	var score int
	var latency int64
	var modelVersion *string
	err := tx.QueryRow(context.Background(), `
		SELECT d.outcome, d.rule_score, d.scoring_latency_us, d.model_version FROM decisions d JOIN purchases p ON p.id = d.purchase_id
		WHERE p.transaction_id = 'pg-5'`).Scan(&outcome, &score, &latency, &modelVersion)
	if err != nil || outcome != "allow" || score != 40 {
		t.Errorf("saved decision = (%s, %d, %v), want (allow, 40)", outcome, score, err)
	}
	if latency <= 0 {
		t.Errorf("saved scoring latency = %d µs, want a positive value", latency)
	}
	if modelVersion != nil || got.ModelVersion != nil {
		t.Errorf("model version = %v saved, %v returned; want none without a model", modelVersion, got.ModelVersion)
	}
}

type deviceHistoryStore struct {
	*storage.PurchaseStore
	calls int
}

func (s *deviceHistoryStore) DeviceCardsLastHour(ctx context.Context, deviceID, cardToken string, at time.Time) (int64, error) {
	s.calls++
	return s.PurchaseStore.DeviceCardsLastHour(ctx, deviceID, cardToken, at)
}

// publishDeviceCardRuleset publishes a ruleset with only the device card
// count rule and checks that it became the active one and left the previous
// version's rules as they were. It returns the new ruleset.
func publishDeviceCardRuleset(t *testing.T, tx pgx.Tx, mux *http.ServeMux, previous scoring.Ruleset) scoring.Ruleset {
	t.Helper()
	ctx := t.Context()
	publish := httptest.NewRequestWithContext(ctx, http.MethodPost, "/rulesets", strings.NewReader(`{
		"review_threshold":20,"block_threshold":80,
		"rules":{"device_card_count":{"enabled":true,"weight":20,"limit":5}}
	}`))
	publish.Header.Set(platform.APIKeyHeader, operatorTestKey)
	published := httptest.NewRecorder()
	mux.ServeHTTP(published, publish)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", published.Code, published.Body)
	}
	active, err := storage.NewRulesetStore(tx).Active(ctx)
	wantConfig := scoring.RuleConfig{Enabled: true, Weight: 20, Limit: 5}
	if err != nil || active.Version <= previous.Version || active.Rules[scoring.RuleDeviceCardCount] != wantConfig {
		t.Fatalf("active ruleset = %+v, %v, want new version with %+v", active, err, wantConfig)
	}
	if n := countRows(t, tx, `SELECT count(*) FROM ruleset_rules WHERE ruleset_version = $1`, previous.Version); n != len(previous.Rules) {
		t.Errorf("prior ruleset has %d rules, want %d", n, len(previous.Rules))
	}
	for name, config := range previous.Rules {
		n := countRows(t, tx, `SELECT count(*) FROM ruleset_rules
			WHERE ruleset_version = $1 AND rule_name = $2 AND enabled = $3 AND weight = $4 AND rule_limit = $5`,
			previous.Version, string(name), config.Enabled, config.Weight, config.Limit)
		if n != 1 {
			t.Errorf("prior ruleset lost original settings for %s", name)
		}
	}
	return active
}

func TestScoreDeviceCardCountWithPostgres(t *testing.T) {
	tx := testTx(t)
	ctx := t.Context()
	rulesetStore := storage.NewRulesetStore(tx)
	purchaseStore := &deviceHistoryStore{PurchaseStore: storage.NewPurchaseStore(tx)}
	mux := http.NewServeMux()
	RegisterRulesetRoutes(mux, NewRulesetHandler(ruleset.NewService(rulesetStore)), platform.Access{ServiceKey: testKey, Operators: testOperators(t)})
	registerScore(mux, newHandler(rulesetStore, storage.NewBlocklistStore(tx), purchaseStore), testKey)

	originalRuleset, err := rulesetStore.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body := func(n int) string {
		return fmt.Sprintf(`{"transaction_id":"device-score-%d","card_token":"device-card-%d",
			"device_id":"device-score","player_id":"device-player","amount_minor":1000,
			"event_time":"2026-11-08T10:00:00Z"}`, n, n)
	}
	first := postScore(ctx, mux, testKey, body(1))
	original := decodeJSON[scoreResponse](t, first)
	if first.Code != http.StatusOK || original.Outcome != scoring.OutcomeAllow || original.Score != 0 ||
		original.RulesetVersion != originalRuleset.Version || purchaseStore.calls != 0 {
		t.Fatalf("score before publishing: %d %s, history calls = %d", first.Code, first.Body, purchaseStore.calls)
	}

	active := publishDeviceCardRuleset(t, tx, mux, originalRuleset)

	for n := 2; n <= 5; n++ {
		rec := postScore(ctx, mux, testKey, body(n))
		got := decodeJSON[scoreResponse](t, rec)
		if rec.Code != http.StatusOK || got.RulesetVersion != active.Version {
			t.Fatalf("score %d: %d %s, want 200 under ruleset %d", n, rec.Code, rec.Body, active.Version)
		}
		if n < 5 && (got.Outcome != scoring.OutcomeAllow || got.Score != 0 || len(got.FiredRules) != 0) {
			t.Errorf("card %d fired below limit: %s", n, rec.Body)
		}
		if n == 5 {
			want := []scoring.FiredRule{{Name: scoring.RuleDeviceCardCount, Points: 20, Message: "Device used 5 distinct cards in 60 minutes (limit 5)"}}
			if got.Outcome != scoring.OutcomeReview || got.Score != 20 || !reflect.DeepEqual(got.FiredRules, want) {
				t.Errorf("card 5: %s, want review with %+v", rec.Body, want)
			}
			retry := postScore(ctx, mux, testKey, body(n))
			if retry.Code != http.StatusOK || retry.Body.String() != rec.Body.String() {
				t.Errorf("card 5 retry: %d %s, want saved rule evidence %s", retry.Code, retry.Body, rec.Body)
			}
		}
	}
	retry := postScore(ctx, mux, testKey, body(1))
	if retry.Code != http.StatusOK || retry.Body.String() != first.Body.String() {
		t.Errorf("pre-activation retry: %d %s, want original response %s", retry.Code, retry.Body, first.Body)
	}
	conflict := postScore(ctx, mux, testKey, strings.Replace(body(1), `"amount_minor":1000`, `"amount_minor":2000`, 1))
	if conflict.Code != http.StatusConflict {
		t.Errorf("conflicting retry: %d %s, want 409", conflict.Code, conflict.Body)
	}
	if purchaseStore.calls != 4 {
		t.Errorf("device history queried %d times, want 4 fresh scores only", purchaseStore.calls)
	}
}

func fullRetryFields() map[string]any {
	return map[string]any{
		"transaction_id": "retry-full", "event_time": "2026-11-08T10:00:00.123456789Z",
		"amount_minor": 75000, "player_id": "retry-player", "card_token": "retry-card",
		"card_issuer": "9500", "card_network": "visa", "card_type": "debit",
		"card_country": "AZ", "card_category": "226", "product_code": "W",
		"billing_region": "299", "billing_country": "87", "billing_distance": 12.5,
		"email_domain": "example.com", "recipient_email_domain": "example.org",
		"device_type": "mobile", "device_name": "phone", "device_id": "retry-device",
		"ip_address": "203.0.113.9", "ip_country": "TR",
		"account_created_at": "2026-11-08T09:00:00.987654321Z",
		"client_os":          "iOS", "client_browser": "Safari", "screen_resolution": "1920x1080",
	}
}

func encodeScoreFields(t *testing.T, fields map[string]any) string {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestScoreRetryComparesStoredPurchase(t *testing.T) {
	tx := testTx(t)
	handler := newHandler(storage.NewRulesetStore(tx), storage.NewBlocklistStore(tx), storage.NewPurchaseStore(tx))
	model := &fakeModel{score: 0.75}
	if err := handler.UseModel(model, testPolicy); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerScore(mux, handler, testKey)
	post := func(fields map[string]any) *httptest.ResponseRecorder {
		return postScore(t.Context(), mux, testKey, encodeScoreFields(t, fields))
	}

	fields := fullRetryFields()
	first := post(fields)
	if first.Code != http.StatusOK {
		t.Fatalf("first score: %d %s", first.Code, first.Body)
	}
	original := decodeJSON[scoreResponse](t, first)
	if original.ModelScore == nil || original.ModelVersion == nil || len(original.FiredRules) == 0 {
		t.Fatalf("first score omitted model or rule evidence: %+v", original)
	}
	model.err = errors.New("model should not be called on retry")
	retry := maps.Clone(fields)
	retry["transaction_id"] = " retry-full "
	retry["card_issuer"] = " 9500 "
	retry["event_time"] = "2026-11-08T14:00:00.123456001+04:00"
	retry["account_created_at"] = "2026-11-08T13:00:00.987654999+04:00"
	retry["ip_address"] = "::ffff:203.0.113.9"
	for _, payload := range []map[string]any{fields, retry} {
		if rec := post(payload); rec.Code != http.StatusOK || rec.Body.String() != first.Body.String() {
			t.Fatalf("retry: %d %s, want %s", rec.Code, rec.Body, first.Body)
		}
	}

	changed := map[string]any{
		"event_time": "2026-11-08T10:00:00.123457Z", "amount_minor": 75001,
		"player_id": "other-player", "card_token": "other-card",
		"card_issuer": "9501", "card_network": "mastercard", "card_type": "credit",
		"card_country": "TR", "card_category": "227", "product_code": "C",
		"billing_region": "300", "billing_country": "88", "billing_distance": 0,
		"email_domain": nil, "recipient_email_domain": "other.example",
		"device_type": "desktop", "device_name": "tablet", "device_id": "other-device",
		"ip_address": "203.0.113.10", "ip_country": "AZ",
		"account_created_at": "2026-11-08T09:00:00.987655Z",
		"client_os":          "Android", "client_browser": "Chrome", "screen_resolution": "1080x1920",
	}
	for name, value := range changed {
		t.Run(name, func(t *testing.T) {
			payload := maps.Clone(fields)
			payload[name] = value
			if rec := post(payload); rec.Code != http.StatusConflict {
				t.Errorf("changed %s: %d %s, want 409", name, rec.Code, rec.Body)
			}
		})
	}
}

func TestScoreRetryReturnsLegacyDecision(t *testing.T) {
	tx := testTx(t)
	ctx := t.Context()
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token)
		VALUES ('txn-1', '2026-11-08T10:00:00Z', 1999, 'player-1', 'card-1')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, model_score)
		VALUES ($1, 1, 'review', 60, '[{"name":"card_velocity","points":60}]', '{}', 0.65)`, id); err != nil {
		t.Fatal(err)
	}
	store := storage.NewPurchaseStore(tx)
	active := fakeRulesets{err: errors.New("scoring should be bypassed")}
	for _, body := range []string{
		minimalBody,
		`{"card_token":"card-1","player_id":"player-1","amount_minor":1999,
		  "event_time":"2026-11-08T10:00:00Z","transaction_id":"txn-1",
		  "email_domain":"  ","device_name":null}`,
	} {
		rec := serve(t, active, store, testKey, body)
		want := scoreResponse{
			TransactionID: "txn-1", RulesetVersion: 1, Outcome: scoring.OutcomeReview,
			Score: 60, FiredRules: []scoring.FiredRule{{Name: scoring.RuleCardVelocity, Points: 60}},
			ModelScore: ptr(0.65),
		}
		if got := decodeJSON[scoreResponse](t, rec); rec.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
			t.Errorf("legacy retry: %d %s, want %+v", rec.Code, rec.Body, want)
		}
	}
}

type synchronizedScoreStore struct {
	*storage.PurchaseStore
	arrived chan<- struct{}
	start   <-chan struct{}
}

func (s synchronizedScoreStore) FindSavedDecision(ctx context.Context, p models.Purchase) (*models.Decision, error) {
	d, err := s.PurchaseStore.FindSavedDecision(ctx, p)
	if d == nil && err == nil {
		s.arrived <- struct{}{}
		select {
		case <-s.start:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return d, err
}

// synchronizedScoreMuxes returns two score handlers on separate connections.
// Each signals on arrived when it reaches the first lookup, and waits there
// until start is closed.
func synchronizedScoreMuxes(t *testing.T, pool *pgxpool.Pool, arrived chan<- struct{}, start <-chan struct{}) [2]*http.ServeMux {
	t.Helper()
	muxes := [2]*http.ServeMux{}
	for i := range muxes {
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(conn.Release)
		store := synchronizedScoreStore{PurchaseStore: storage.NewPurchaseStore(conn), arrived: arrived, start: start}
		muxes[i] = http.NewServeMux()
		registerScore(muxes[i], newHandler(storage.NewRulesetStore(conn), storage.NewBlocklistStore(conn), store), testKey)
	}
	return muxes
}

// countScoreStatuses reads n responses and counts the 200 and 409 ones. Every
// 200 response must carry the same decision.
func countScoreStatuses(t *testing.T, responses <-chan *httptest.ResponseRecorder, n int) (ok, conflicts int) {
	t.Helper()
	var winner string
	for range n {
		rec := <-responses
		switch rec.Code {
		case http.StatusOK:
			ok++
			if winner != "" && rec.Body.String() != winner {
				t.Errorf("matching requests returned different decisions: %s and %s", winner, rec.Body)
			}
			winner = rec.Body.String()
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status %d: %s", rec.Code, rec.Body)
		}
	}
	return ok, conflicts
}

// checkOnePurchaseSaved checks that the database holds one purchase with its
// decision, counted once in each of the six running totals.
func checkOnePurchaseSaved(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if n := countRows(t, pool, "SELECT count(*) FROM purchases"); n != 1 {
		t.Errorf("purchases = %d, want 1", n)
	}
	if n := countRows(t, pool, "SELECT count(*) FROM decisions"); n != 1 {
		t.Errorf("decisions = %d, want 1", n)
	}
	if n := countRows(t, pool, "SELECT count(*) FROM feature_counts WHERE purchases = 1 AND players = 1"); n != 6 {
		t.Errorf("feature counts incremented once = %d, want 6", n)
	}
	if n := countRows(t, pool, "SELECT count(*) FROM feature_counts"); n != 6 {
		t.Errorf("feature count rows = %d, want 6", n)
	}
}

func TestConcurrentScoreRetries(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  bool
		wantOK  int
		want409 int
	}{
		{"matching", false, 2, 0},
		{"conflicting", true, 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := testIsolatedPool(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			arrived := make(chan struct{}, 4)
			start := make(chan struct{})
			muxes := synchronizedScoreMuxes(t, pool, arrived, start)

			fields := fullRetryFields()
			firstBody := encodeScoreFields(t, fields)
			if test.change {
				fields["amount_minor"] = 500
			}
			bodies := [2]string{firstBody, encodeScoreFields(t, fields)}
			responses := make(chan *httptest.ResponseRecorder, len(muxes))
			var workers sync.WaitGroup
			defer func() {
				cancel()
				workers.Wait()
			}()
			for i, mux := range muxes {
				workers.Go(func() { responses <- postScore(ctx, mux, testKey, bodies[i]) })
			}
			for range muxes {
				select {
				case <-arrived:
				case <-ctx.Done():
					t.Fatal("both score requests did not reach the initial lookup")
				}
			}
			close(start)

			ok, conflicts := countScoreStatuses(t, responses, len(muxes))
			if ok != test.wantOK || conflicts != test.want409 {
				t.Errorf("got %d successes and %d conflicts, want %d and %d", ok, conflicts, test.wantOK, test.want409)
			}
			checkOnePurchaseSaved(t, pool)
		})
	}
}

type scoreTestHandler struct {
	*ScoreHandler
	service *score.Service
}

func newHandler(r score.Rulesets, b score.Blocklist, p score.Purchases) *scoreTestHandler {
	svc := score.NewService(r, b, p)
	return &scoreTestHandler{ScoreHandler: NewScoreHandler(svc), service: svc}
}

func (h *scoreTestHandler) UseModel(m score.Model, p scoring.ModelPolicy) error {
	return h.service.UseModel(m, p)
}

func registerScore(mux *http.ServeMux, h *scoreTestHandler, apiKey string) {
	RegisterScoreRoutes(mux, h.ScoreHandler, apiKey)
}
