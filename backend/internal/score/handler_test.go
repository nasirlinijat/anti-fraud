package score

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/rulesets"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const testKey = "test-key"

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
}

type fakeRulesets struct {
	rs  scoring.Ruleset
	err error
}

func (f fakeRulesets) Active(context.Context) (scoring.Ruleset, error) { return f.rs, f.err }

type fakePurchases struct {
	cardUses     int
	countErr     error
	historyErr   error
	earlierSum   float64
	earlierCount int64
	earlierErr   error
	history      features.History
	saveErr      error
	countCard    string
	countAt      time.Time
	saved        []Purchase
	decisions    []Decision
}

func (f *fakePurchases) CardPurchasesLastHour(_ context.Context, card string, at time.Time) (int, error) {
	f.countCard, f.countAt = card, at
	return f.cardUses, f.countErr
}

func (f *fakePurchases) FeatureHistory(context.Context, features.Input) (features.History, error) {
	return f.history, f.historyErr
}

func (f *fakePurchases) PlayerEarlierModelScores(context.Context, string, time.Time, string) (float64, int64, error) {
	return f.earlierSum, f.earlierCount, f.earlierErr
}

func (f *fakePurchases) Save(_ context.Context, p Purchase, d Decision) (int64, error) {
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
	entry   blocklist.Entry
	matched bool
	err     error
	subject blocklist.Subject
}

func (f *fakeBlocklist) Match(_ context.Context, s blocklist.Subject) (blocklist.Entry, bool, error) {
	f.subject = s
	return f.entry, f.matched, f.err
}

// serve sends a request to a handler whose blocklist is empty.
func serve(t *testing.T, r Rulesets, p Purchases, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveWith(t, r, &fakeBlocklist{}, p, key, body)
}

func serveWith(t *testing.T, r Rulesets, b Blocklist, p Purchases, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHandler(r, b, p), testKey)
	req := httptest.NewRequest(http.MethodPost, "/score", strings.NewReader(body))
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

	wantPurchase := Purchase{
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
			{Name: scoring.RuleHighAmount, Points: 30},
			{Name: scoring.RuleCardVelocity, Points: 40},
			{Name: scoring.RuleNewAccount, Points: 20},
			{Name: scoring.RuleCountryMismatch, Points: 25},
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
	rec := serve(t, fakeRulesets{rs: testRuleset}, &fakePurchases{}, testKey, body)

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
		if len(purchases.saved) != 0 {
			t.Errorf("key %q: purchase was saved", key)
		}
	}
}

func TestScoreDuplicateTransaction(t *testing.T) {
	rec := serve(t, fakeRulesets{rs: testRuleset}, &fakePurchases{saveErr: ErrDuplicateTransaction}, testKey, minimalBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if got := decodeJSON[platform.ErrorResponse](t, rec); got.Fields["transaction_id"] == "" {
		t.Errorf("response %+v does not name transaction_id", got)
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

func wantBlocklistSubject() blocklist.Subject {
	addr := netip.MustParseAddr("203.0.113.9")
	return blocklist.Subject{CardToken: "card-9", PlayerID: "player-9", DeviceID: ptr("device-9"), IPAddress: &addr}
}

func TestScoreBlocksBlocklistMatch(t *testing.T) {
	purchases := &fakePurchases{}
	bl := &fakeBlocklist{entry: blocklist.Entry{Type: blocklist.TypeCard, Value: "card-9", Reason: "chargebacks"}, matched: true}
	rec := serveWith(t, fakeRulesets{rs: testRuleset}, bl, purchases, testKey, blocklistBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[scoreResponse](t, rec)
	want := scoreResponse{
		TransactionID:  "txn-bl",
		Outcome:        scoring.OutcomeBlock,
		FiredRules:     []scoring.FiredRule{},
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
	RegisterRoutes(mux, NewHandler(rulesets.NewStore(tx), blocklist.NewStore(tx), NewStore(tx)), testKey)
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
	h := NewHandler(rulesets.NewStore(tx), blocklist.NewStore(tx), NewStore(tx))
	mux := http.NewServeMux()
	RegisterRoutes(mux, h, testKey)

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
	want := []scoring.FiredRule{{Name: scoring.RuleCardVelocity, Points: 40}}
	if rec.Code != http.StatusOK || !reflect.DeepEqual(got.FiredRules, want) || got.RulesetVersion != 1 {
		t.Errorf("sixth purchase: status %d, response %+v, want card_velocity fired under ruleset 1", rec.Code, got)
	}

	if rec := post(body(5)); rec.Code != http.StatusConflict {
		t.Errorf("repeated transaction: status %d, want 409", rec.Code)
	}

	var outcome string
	var score int
	err := tx.QueryRow(context.Background(), `
		SELECT d.outcome, d.rule_score FROM decisions d JOIN purchases p ON p.id = d.purchase_id
		WHERE p.transaction_id = 'pg-5'`).Scan(&outcome, &score)
	if err != nil || outcome != "allow" || score != 40 {
		t.Errorf("saved decision = (%s, %d, %v), want (allow, 40)", outcome, score, err)
	}
}
