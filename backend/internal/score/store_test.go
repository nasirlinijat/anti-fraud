package score

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func testTx(t *testing.T) pgx.Tx {
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

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(ctx) })
	return tx
}

func ptr[T any](v T) *T { return &v }

func minimalPurchase(transactionID string) Purchase {
	return Purchase{
		TransactionID: transactionID,
		EventTime:     time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
		AmountMinor:   1999,
		PlayerID:      "player-1",
		CardToken:     "card-1",
	}
}

func countRows(t *testing.T, tx pgx.Tx, query string, args ...any) int {
	t.Helper()
	var n int
	if err := tx.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestSaveStoresPurchaseAndDecision(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	baku := time.FixedZone("Baku", 4*3600)
	p := Purchase{
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
	}
	d := Decision{
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

	id, err := NewStore(tx).Save(ctx, p, d)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	var (
		got        Purchase
		ip         netip.Addr
		accountAt  time.Time
		version    int
		outcome    string
		ruleScore  int
		firedJSON  []byte
		inputsJSON []byte
		modelScore float64
	)
	err = tx.QueryRow(ctx, `
		SELECT p.transaction_id, p.event_time, p.amount_minor, p.player_id, p.card_token,
		       p.card_issuer, p.card_network, p.card_type, p.card_country, p.card_category,
		       p.product_code, p.billing_region, p.billing_country, p.billing_distance,
		       p.email_domain, p.recipient_email_domain, p.device_type, p.device_name, p.device_id,
		       p.ip_address, p.ip_country, p.account_created_at,
		       d.ruleset_version, d.outcome, d.rule_score, d.fired_rules, d.model_inputs, d.model_score
		FROM purchases p JOIN decisions d ON d.purchase_id = p.id
		WHERE p.id = $1`, id).Scan(
		&got.TransactionID, &got.EventTime, &got.AmountMinor, &got.PlayerID, &got.CardToken,
		&got.CardIssuer, &got.CardNetwork, &got.CardType, &got.CardCountry, &got.CardCategory,
		&got.ProductCode, &got.BillingRegion, &got.BillingCountry, &got.BillingDistance,
		&got.EmailDomain, &got.RecipientEmailDomain, &got.DeviceType, &got.DeviceName, &got.DeviceID,
		&ip, &got.IPCountry, &accountAt,
		&version, &outcome, &ruleScore, &firedJSON, &inputsJSON, &modelScore,
	)
	if err != nil {
		t.Fatalf("read saved purchase: %v", err)
	}
	got.IPAddress = &ip
	got.AccountCreatedAt = &accountAt

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

	if version != 1 || outcome != "review" || ruleScore != 55 || modelScore != 0.42 {
		t.Errorf("decision = (version %d, %s, score %d, model %v), want (version 1, review, score 55, model 0.42)",
			version, outcome, ruleScore, modelScore)
	}
	var fired []scoring.FiredRule
	if err := json.Unmarshal(firedJSON, &fired); err != nil {
		t.Fatalf("decode fired_rules %s: %v", firedJSON, err)
	}
	if !reflect.DeepEqual(fired, d.Result.FiredRules) {
		t.Errorf("fired_rules = %+v, want %+v", fired, d.Result.FiredRules)
	}
	var inputs map[string]float64
	if err := json.Unmarshal(inputsJSON, &inputs); err != nil {
		t.Fatalf("decode model_inputs %s: %v", inputsJSON, err)
	}
	if !reflect.DeepEqual(inputs, d.ModelInputs) {
		t.Errorf("model_inputs = %v, want %v", inputs, d.ModelInputs)
	}
}

func TestSaveWithoutOptionalFields(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	d := Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}
	id, err := NewStore(tx).Save(ctx, minimalPurchase("txn-minimal"), d)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	n := countRows(t, tx, `
		SELECT count(*) FROM purchases p JOIN decisions d ON d.purchase_id = p.id
		WHERE p.id = $1
		  AND num_nonnulls(p.card_issuer, p.card_network, p.card_type, p.card_country, p.card_category,
		                   p.product_code, p.billing_region, p.billing_country, p.billing_distance,
		                   p.email_domain, p.recipient_email_domain, p.device_type, p.device_name,
		                   p.device_id, p.ip_address, p.ip_country, p.account_created_at) = 0
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
	store := NewStore(tx)

	_, err := store.Save(ctx, minimalPurchase("txn-bad-version"),
		Decision{RulesetVersion: 9999, Result: scoring.Result{Outcome: scoring.OutcomeAllow}})
	if err == nil {
		t.Fatal("Save with an unknown ruleset version succeeded, want an error")
	}
	if n := countRows(t, tx, `SELECT count(*) FROM purchases WHERE transaction_id = 'txn-bad-version'`); n != 0 {
		t.Errorf("purchase was saved without its decision")
	}
}

func TestSaveRejectsDuplicateTransaction(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	store := NewStore(tx)
	d := Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}}

	if _, err := store.Save(ctx, minimalPurchase("txn-dup"), d); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := store.Save(ctx, minimalPurchase("txn-dup"), d); err == nil {
		t.Error("second Save with the same transaction ID succeeded, want an error")
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
