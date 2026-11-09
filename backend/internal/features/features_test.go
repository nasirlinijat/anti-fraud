package features_test

import (
	"context"
	"encoding/csv"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

const (
	transactionsPath = "../../../ml/testdata/transactions.csv"
	expectedPath     = "../../../ml/testdata/expected_features.csv"
	modelPath        = "../../models/lgbm-ieee-v4.json"
	tolerance        = 1e-9
)

func ptr[T any](v T) *T { return &v }

func TestNamesMatchModel(t *testing.T) {
	model, err := lgbm.Load(modelPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := model.Features(); !reflect.DeepEqual(got, features.Names) {
		t.Fatalf("model features = %v, want %v", got, features.Names)
	}
}

func TestVectorWithoutHistory(t *testing.T) {
	in := features.Input{
		TransactionID: "txn-1",
		EventTime:     time.Date(2026, 11, 9, 13, 45, 0, 0, time.FixedZone("Baku", 4*3600)),
		AmountMinor:   12345,
		PlayerID:      "player-1",
		CardToken:     "card-1",
		CardNetwork:   ptr("mastercard"),
		CardType:      ptr("prepaid"),
		CardCountry:   ptr("AZ"),
		CardCategory:  ptr("226"),
		ProductCode:   ptr("C"),
		DeviceType:    ptr("mobile"),
		// Created 3 days and 20 hours before the purchase: 3 whole days.
		AccountCreatedAt: ptr(time.Date(2026, 11, 5, 13, 45, 0, 0, time.UTC)),
		ClientOS:         ptr("Mac OS X 10_13"),
		ClientBrowser:    ptr("Mobile Safari 11.0"),
		ScreenResolution: ptr("1334x750"),
	}
	got := features.Map(features.Vector(in, features.History{}))
	want := map[string]float64{
		"amount":                   123.45,
		"cents":                    0.45,
		"product_code":             2,
		"card_network":             1,
		"card_type":                features.Missing,
		"player_txn_count":         0,
		"player_avg_amount":        features.Missing,
		"amount_to_avg":            features.Missing,
		"secs_since_last":          features.Missing,
		"secs_since_first":         features.Missing,
		"email_is_new_for_player":  0,
		"card_country":             features.Missing,
		"card_category":            226,
		"billing_country":          features.Missing,
		"email_domain_share":       features.Missing,
		"has_recipient_email":      0,
		"recipient_email_differs":  0,
		"player_max_amount":        features.Missing,
		"email_players_share":      features.Missing,
		"has_device_info":          1,
		"device_type":              1,
		"device_info_share":        features.Missing,
		"device_players_share":     features.Missing,
		"device_is_new_for_player": 0,
		"billing_distance":         features.Missing,
		"account_age_days":         3,
		"has_client_info":          1,
		"os_family":                2,
		"browser_family":           1,
		"os_share":                 features.Missing,
		"screen_area":              1334 * 750,
		"os_is_new_for_player":     0,
		"player_distinct_browsers": 0,
	}
	if len(got) != len(features.Names) {
		t.Fatalf("got %d features, want %d", len(got), len(features.Names))
	}
	for name, w := range want {
		if math.Abs(got[name]-w) > tolerance {
			t.Errorf("%s = %v, want %v", name, got[name], w)
		}
	}
}

func TestVectorRecipientEmail(t *testing.T) {
	tests := []struct {
		name               string
		email, recipient   *string
		hasRecipient, diff float64
	}{
		{"none", nil, nil, 0, 0},
		{"same", ptr("gmail.com"), ptr("gmail.com"), 1, 0},
		{"different", ptr("gmail.com"), ptr("yahoo.com"), 1, 1},
		{"no purchaser email", nil, ptr("yahoo.com"), 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := features.Input{AmountMinor: 100, EmailDomain: tt.email, RecipientEmailDomain: tt.recipient}
			got := features.Map(features.Vector(in, features.History{}))
			if got["has_recipient_email"] != tt.hasRecipient || got["recipient_email_differs"] != tt.diff {
				t.Errorf("has_recipient_email %v, recipient_email_differs %v; want %v, %v",
					got["has_recipient_email"], got["recipient_email_differs"], tt.hasRecipient, tt.diff)
			}
		})
	}
}

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

	// Start from an empty history inside the transaction; the rollback restores it.
	for _, table := range []string{"decisions", "purchases", "feature_counts"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("empty %s: %v", table, err)
		}
	}
	return tx
}

func readCSV(t *testing.T, path string) ([]string, []map[string]string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	header := records[0]
	var rows []map[string]string
	for _, record := range records[1:] {
		row := map[string]string{}
		for i, name := range header {
			row[name] = record[i]
		}
		rows = append(rows, row)
	}
	return header, rows
}

// purchaseFromIEEE converts a test row in IEEE-CIS format into a purchase the
// way the replay does: event_time counts seconds from the Unix epoch, and the
// player and card IDs are built as in ml/features.py.
func purchaseFromIEEE(t *testing.T, row map[string]string) score.Purchase {
	t.Helper()
	optional := func(column string) *string {
		if row[column] == "" {
			return nil
		}
		return ptr(row[column])
	}
	key := func(column string) string {
		if row[column] == "" {
			return "none"
		}
		return row[column]
	}

	seconds, err := strconv.ParseInt(row["TransactionDT"], 10, 64)
	if err != nil {
		t.Fatalf("TransactionDT %q: %v", row["TransactionDT"], err)
	}
	amount, err := strconv.ParseFloat(row["TransactionAmt"], 64)
	if err != nil {
		t.Fatalf("TransactionAmt %q: %v", row["TransactionAmt"], err)
	}
	firstDay := "none"
	if row["D1"] != "" {
		d1, err := strconv.ParseInt(row["D1"], 10, 64)
		if err != nil {
			t.Fatalf("D1 %q: %v", row["D1"], err)
		}
		firstDay = strconv.FormatInt(seconds/86400-d1, 10)
	}
	var card []string
	for _, column := range []string{"card1", "card2", "card3", "card4", "card5", "card6"} {
		card = append(card, key(column))
	}

	p := score.Purchase{
		TransactionID:        row["TransactionID"],
		EventTime:            time.Unix(seconds, 0).UTC(),
		AmountMinor:          int64(math.Round(amount * 100)),
		PlayerID:             key("card1") + "_" + key("addr1") + "_" + firstDay,
		CardToken:            strings.Join(card, "|"),
		CardIssuer:           optional("card1"),
		CardNetwork:          optional("card4"),
		CardType:             optional("card6"),
		CardCountry:          optional("card3"),
		CardCategory:         optional("card5"),
		ProductCode:          optional("ProductCD"),
		BillingRegion:        optional("addr1"),
		BillingCountry:       optional("addr2"),
		EmailDomain:          optional("P_emaildomain"),
		RecipientEmailDomain: optional("R_emaildomain"),
		DeviceType:           optional("DeviceType"),
		DeviceName:           optional("DeviceInfo"),
		ClientOS:             optional("id_30"),
		ClientBrowser:        optional("id_31"),
		ScreenResolution:     optional("id_33"),
	}
	// D1 counts the days since the client began, the IEEE stand-in for account age.
	if row["D1"] != "" {
		d1, err := strconv.ParseInt(row["D1"], 10, 64)
		if err != nil {
			t.Fatalf("D1 %q: %v", row["D1"], err)
		}
		created := p.EventTime.Add(-time.Duration(d1) * 24 * time.Hour)
		p.AccountCreatedAt = &created
	}
	if row["dist1"] != "" {
		d, err := strconv.ParseFloat(row["dist1"], 64)
		if err != nil {
			t.Fatalf("dist1 %q: %v", row["dist1"], err)
		}
		p.BillingDistance = &d
	}
	return p
}

// TestHistoryMatchesExpectedFeatures scores the AF-15 test rows one by one,
// saving each after it is scored, and compares every feature with the values
// ml/features.py computed for the same rows.
func TestHistoryMatchesExpectedFeatures(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	store := score.NewStore(tx)

	_, transactions := readCSV(t, transactionsPath)
	header, expected := readCSV(t, expectedPath)
	if want := append([]string{"TransactionID"}, features.Names...); !reflect.DeepEqual(header, want) {
		t.Fatalf("expected features header = %v, want %v", header, want)
	}
	if len(transactions) != len(expected) || len(transactions) == 0 {
		t.Fatalf("%d transactions and %d expected rows", len(transactions), len(expected))
	}

	for i, row := range transactions {
		p := purchaseFromIEEE(t, row)
		want := expected[i]
		if want["TransactionID"] != p.TransactionID {
			t.Fatalf("row %d: expected features are for transaction %s, not %s", i, want["TransactionID"], p.TransactionID)
		}

		in := p.FeatureInput()
		history, err := store.FeatureHistory(ctx, in)
		if err != nil {
			t.Fatalf("transaction %s: FeatureHistory: %v", p.TransactionID, err)
		}
		values := features.Vector(in, history)
		for j, name := range features.Names {
			w, err := strconv.ParseFloat(want[name], 64)
			if err != nil {
				t.Fatalf("transaction %s: expected %s %q: %v", p.TransactionID, name, want[name], err)
			}
			if math.Abs(values[j]-w) > tolerance {
				t.Errorf("transaction %s: %s = %v, want %v", p.TransactionID, name, values[j], w)
			}
		}

		_, err = store.Save(ctx, p, score.Decision{
			RulesetVersion: 1,
			Result:         scoring.Result{Outcome: scoring.OutcomeAllow},
			ModelInputs:    features.Map(values),
			FeatureHistory: history,
		})
		if err != nil {
			t.Fatalf("transaction %s: Save: %v", p.TransactionID, err)
		}
	}
}

type countRow struct {
	Kind, Key          string
	Purchases, Players int64
}

func TestRunningTotals(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	store := score.NewStore(tx)

	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	purchases := []score.Purchase{
		{TransactionID: "a", PlayerID: "p1", EmailDomain: ptr("gmail.com"), DeviceName: ptr("Windows")},
		{TransactionID: "b", PlayerID: "p1", EmailDomain: ptr("gmail.com"), DeviceName: ptr("Windows")},
		{TransactionID: "c", PlayerID: "p2", EmailDomain: ptr("gmail.com")},
		{TransactionID: "d", PlayerID: "p2", DeviceName: ptr("Windows")},
	}
	for i, p := range purchases {
		p.EventTime = at.Add(time.Duration(i) * time.Minute)
		p.AmountMinor = 1000
		p.CardToken = "card-" + p.PlayerID
		h, err := store.FeatureHistory(ctx, p.FeatureInput())
		if err != nil {
			t.Fatalf("FeatureHistory: %v", err)
		}
		d := score.Decision{RulesetVersion: 1, Result: scoring.Result{Outcome: scoring.OutcomeAllow}, FeatureHistory: h}
		if _, err := store.Save(ctx, p, d); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	rows, err := tx.Query(ctx, "SELECT kind, key, purchases, players FROM feature_counts ORDER BY kind, key")
	if err != nil {
		t.Fatalf("read feature_counts: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[countRow])
	if err != nil {
		t.Fatalf("collect feature_counts: %v", err)
	}
	want := []countRow{
		{"all", "", 4, 2},
		{"device", "Windows", 3, 2},
		{"email", "gmail.com", 3, 2},
		{"email", "none", 1, 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("feature_counts = %+v, want %+v", got, want)
	}
}
