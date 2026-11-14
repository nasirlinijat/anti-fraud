package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testTx(t *testing.T) pgx.Tx {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(ctx) })
	return tx
}

func ptr[T any](v T) *T { return &v }

func minimalPurchase(transactionID string) models.Purchase {
	return models.Purchase{
		TransactionID: transactionID,
		EventTime:     time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
		AmountMinor:   1999,
		PlayerID:      "player-1",
		CardToken:     "card-1",
	}
}

func countRows(t *testing.T, conn Conn, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// fullPurchase returns a purchase with every optional field set. Its event
// time is in a zone other than UTC.
func fullPurchase() models.Purchase {
	baku := time.FixedZone("Baku", 4*3600)
	return models.Purchase{
		TransactionID:        "txn-full",
		EventTime:            time.Date(2026, 11, 8, 14, 30, 0, 0, baku),
		AmountMinor:          75000,
		PlayerID:             "player-7",
		CardToken:            "card-7",
		CardIssuer:           ptr("1001"),
		CardNetwork:          ptr("visa"),
		CardType:             ptr("debit"),
		CardCountry:          ptr("AZ"),
		CardCategory:         ptr("226"),
		ProductCode:          ptr("W"),
		BillingRegion:        ptr("100"),
		BillingCountry:       ptr("87"),
		BillingDistance:      ptr(12.5),
		EmailDomain:          ptr("gmail.com"),
		RecipientEmailDomain: ptr("yahoo.com"),
		DeviceType:           ptr("mobile"),
		DeviceName:           ptr("iOS Device"),
		DeviceID:             ptr("device-7"),
		IPAddress:            ptr(netip.MustParseAddr("203.0.113.7")),
		IPCountry:            ptr("TR"),
		AccountCreatedAt:     ptr(time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)),
		CVVResult:            ptr(scoring.CVVMatch),
		AVSResult:            ptr(scoring.AVSPartial),
		ThreeDSResult:        ptr(scoring.ThreeDSFailed),
	}
}

// savedDecision holds the decision columns as they were saved.
type savedDecision struct {
	version    int
	outcome    string
	ruleScore  int
	fired      []scoring.FiredRule
	inputs     map[string]float64
	modelScore float64
}

// readSaved reads back a purchase that has every optional field, and its
// decision.
func readSaved(t *testing.T, tx pgx.Tx, id int64) (models.Purchase, savedDecision) {
	t.Helper()
	ctx := context.Background()
	var (
		got        models.Purchase
		d          savedDecision
		ip         netip.Addr
		accountAt  time.Time
		firedJSON  []byte
		inputsJSON []byte
	)
	err := tx.QueryRow(ctx, `
		SELECT p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.card_token,
		       p.card_issuer, p.card_network, p.card_type, p.card_country, p.card_category,
		       p.product_code, p.billing_region, p.billing_country, p.billing_distance,
		       p.email_domain, p.recipient_email_domain, p.device_type, p.device_name, p.device_id,
		       p.ip_address, p.ip_country, p.account_created_at,
		       p.cvv_result, p.avs_result, p.three_ds_result,
		       d.ruleset_version, d.outcome, d.rule_score, d.fired_rules, d.model_inputs, d.model_score
		FROM purchases p JOIN decisions d ON d.purchase_id = p.id
		WHERE p.id = $1`, id).Scan(
		&got.TransactionID, &got.EventTime, &got.AmountMinor, &got.PlayerID, &got.CardToken,
		&got.CardIssuer, &got.CardNetwork, &got.CardType, &got.CardCountry, &got.CardCategory,
		&got.ProductCode, &got.BillingRegion, &got.BillingCountry, &got.BillingDistance,
		&got.EmailDomain, &got.RecipientEmailDomain, &got.DeviceType, &got.DeviceName, &got.DeviceID,
		&ip, &got.IPCountry, &accountAt,
		&got.CVVResult, &got.AVSResult, &got.ThreeDSResult,
		&d.version, &d.outcome, &d.ruleScore, &firedJSON, &inputsJSON, &d.modelScore,
	)
	if err != nil {
		t.Fatalf("read saved purchase: %v", err)
	}
	got.IPAddress = &ip
	got.AccountCreatedAt = &accountAt
	if err := json.Unmarshal(firedJSON, &d.fired); err != nil {
		t.Fatalf("decode fired_rules %s: %v", firedJSON, err)
	}
	if err := json.Unmarshal(inputsJSON, &d.inputs); err != nil {
		t.Fatalf("decode model_inputs %s: %v", inputsJSON, err)
	}
	return got, d
}

func TestSaveStoresPurchaseAndDecision(t *testing.T) {
	tx := testTx(t)
	p := fullPurchase()
	d := models.Decision{
		RulesetVersion: 1,
		Result: scoring.Result{
			Outcome: scoring.OutcomeReview,
			Score:   55,
			FiredRules: []scoring.FiredRule{
				{Name: scoring.RuleHighAmount, Points: 30},
				{Name: scoring.RuleCountryMismatch, Points: 25},
			},
		},
		ModelInputs: map[string]float64{"amount": 750, "card_uses_1h": 2, "device_type": 1},
		ModelScore:  ptr(0.42),
	}

	id, err := NewPurchaseStore(tx).Save(context.Background(), p, d)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, saved := readSaved(t, tx, id)

	if !got.EventTime.Equal(p.EventTime) {
		t.Errorf("event_time = %v, want %v", got.EventTime, p.EventTime)
	}
	if got.EventTime.UTC() != time.Date(2026, 11, 8, 10, 30, 0, 0, time.UTC) {
		t.Errorf("event_time in UTC = %v, want 2026-11-08 10:30 UTC", got.EventTime.UTC())
	}
	got.EventTime = p.EventTime
	if !got.AccountCreatedAt.Equal(*p.AccountCreatedAt) {
		t.Errorf("account_created_at = %v, want %v", got.AccountCreatedAt, p.AccountCreatedAt)
	}
	got.AccountCreatedAt = p.AccountCreatedAt
	if !reflect.DeepEqual(got, p) {
		t.Errorf("saved purchase = %+v, want %+v", got, p)
	}

	if saved.version != 1 || saved.outcome != "review" || saved.ruleScore != 55 || saved.modelScore != 0.42 {
		t.Errorf("decision = (version %d, %s, score %d, model %v), want (version 1, review, score 55, model 0.42)",
			saved.version, saved.outcome, saved.ruleScore, saved.modelScore)
	}
	if !reflect.DeepEqual(saved.fired, d.Result.FiredRules) {
		t.Errorf("fired_rules = %+v, want %+v", saved.fired, d.Result.FiredRules)
	}
	if !reflect.DeepEqual(saved.inputs, d.ModelInputs) {
		t.Errorf("model_inputs = %v, want %v", saved.inputs, d.ModelInputs)
	}
}

func TestSaveWithoutOptionalFields(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	d := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}
	id, err := NewPurchaseStore(tx).Save(ctx, minimalPurchase("txn-minimal"), d)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	n := countRows(t, tx, `
		SELECT count(*) FROM purchases p JOIN decisions d ON d.purchase_id = p.id
		WHERE p.id = $1
		  AND num_nonnulls(p.card_issuer, p.card_network, p.card_type, p.card_country, p.card_category,
		                   p.product_code, p.billing_region, p.billing_country, p.billing_distance,
		                   p.email_domain, p.recipient_email_domain, p.device_type, p.device_name,
		                   p.device_id, p.ip_address, p.ip_country, p.account_created_at,
		                   p.cvv_result, p.avs_result, p.three_ds_result) = 0
		  AND d.model_score IS NULL
		  AND d.fired_rules = '[]'::jsonb
		  AND d.model_inputs = '{}'::jsonb`, id)
	if n != 1 {
		t.Error("optional fields, model score, fired rules, or model inputs were not saved as empty")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	store := NewPurchaseStore(tx)

	_, err := store.Save(ctx, minimalPurchase("txn-bad-version"),
		models.Decision{RulesetVersion: 9999, Result: scoring.Result{Outcome: scoring.OutcomeAllow}})
	if err == nil {
		t.Fatal("Save with an unknown ruleset version succeeded, want an error")
	}
	if n := countRows(t, tx, `SELECT count(*) FROM purchases WHERE transaction_id = 'txn-bad-version'`); n != 0 {
		t.Errorf("purchase was saved without its decision")
	}
	p := minimalPurchase("txn-bad-version")
	if _, err := store.Save(ctx, p, models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if got, err := store.FindSavedDecision(ctx, p); err != nil || got == nil || got.Result.Outcome != scoring.OutcomeAllow {
		t.Errorf("lookup after successful retry = %+v, %v", got, err)
	}
}

func TestSaveRejectsDuplicateTransaction(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	store := NewPurchaseStore(tx)
	d := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}

	if _, err := store.Save(ctx, minimalPurchase("txn-dup"), d); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := store.Save(ctx, minimalPurchase("txn-dup"), d); !errors.Is(err, models.ErrDuplicateTransaction) {
		t.Errorf("second Save error = %v, want models.ErrDuplicateTransaction", err)
	}
}

func TestCardPurchasesLastHour(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	store := NewPurchaseStore(tx)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	d := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}

	saved := []struct {
		id   string
		card string
		at   time.Time
	}{
		{"v-exactly-1h", "card-v", now.Add(-time.Hour)},
		{"v-59m", "card-v", now.Add(-59 * time.Minute)},
		{"v-1m", "card-v", now.Add(-time.Minute)},
		{"v-same-second", "card-v", now},
		{"v-later", "card-v", now.Add(time.Minute)},
		{"v-other-card", "card-other", now.Add(-time.Minute)},
	}
	for _, s := range saved {
		p := minimalPurchase(s.id)
		p.CardToken = s.card
		p.EventTime = s.at
		if _, err := store.Save(ctx, p, d); err != nil {
			t.Fatalf("Save %s: %v", s.id, err)
		}
	}

	got, err := store.CardPurchasesLastHour(ctx, "card-v", now)
	if err != nil {
		t.Fatalf("CardPurchasesLastHour: %v", err)
	}
	// 59m, 1m, and same second. Exactly one hour earlier, later, and other cards are not counted.
	if got.Count != 3 || len(got.PurchaseIDs) != 3 {
		t.Errorf("CardPurchasesLastHour = %d, want 3", got)
	}
}

func TestCardHistoryKeepsTheCountWhenReferencesExceedTheLimit(t *testing.T) {
	tx := testTx(t)
	store := NewPurchaseStore(tx)
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	_, err := tx.Exec(t.Context(), `
		INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token)
		SELECT 'velocity-limit-' || n, $1, 499, 'velocity-player', 'velocity-limit-card'
		FROM generate_series(1, 51) AS n`, at)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.CardPurchasesLastHour(t.Context(), "velocity-limit-card", at)
	if err != nil || got.Count != 51 || len(got.PurchaseIDs) != 0 {
		t.Fatalf("history = %+v, %v; want a count of 51 with no references", got, err)
	}
}

func TestDeviceCardsLastHour(t *testing.T) {
	tx := testTx(t)
	store := NewPurchaseStore(tx)
	ctx := t.Context()
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	device := "device-count"
	decision := models.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}
	for i, row := range []struct {
		device *string
		card   string
		offset time.Duration
	}{
		{&device, "outside", -time.Hour},
		{&device, "earliest", -time.Hour + time.Microsecond},
		{&device, "earliest", -time.Minute},
		{&device, "same-time", 0},
		{&device, "current", -time.Minute},
		{&device, "future", time.Microsecond},
		{ptr("other-device"), "other-card", -time.Minute},
		{nil, "unknown-device-card", -time.Minute},
	} {
		purchase := minimalPurchase(fmt.Sprintf("device-count-%d", i))
		purchase.DeviceID = row.device
		purchase.CardToken = row.card
		purchase.EventTime = at.Add(row.offset)
		if _, err := store.Save(ctx, purchase, decision); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		device string
		card   string
		want   int64
	}{
		{device, "current", 3},
		{device, "new-card", 4},
		{"unused-device", "new-card", 1},
	} {
		if got, err := store.DeviceCardsLastHour(ctx, tc.device, tc.card, at); err != nil || got != tc.want {
			t.Errorf("count for %s and %s = %d, %v, want %d", tc.device, tc.card, got, err, tc.want)
		}
	}
}

func TestNoFraudLabelColumns(t *testing.T) {
	tx := testTx(t)
	n := countRows(t, tx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name IN ('purchases', 'decisions')
		  AND (column_name LIKE '%fraud%' OR column_name LIKE '%label%')`)
	if n != 0 {
		t.Errorf("found %d fraud label columns in purchases or decisions, want none", n)
	}
}
