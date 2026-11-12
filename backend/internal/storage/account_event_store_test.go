package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

var accountEventBase = time.Date(2000, 6, 1, 12, 0, 0, 0, time.UTC)

func accountEvent(id, playerID string, eventType models.AccountEventType, at time.Time, deviceID, ipCountry *string) models.AccountEvent {
	return models.AccountEvent{EventID: id, PlayerID: playerID, EventType: eventType, EventTime: at, DeviceID: deviceID, IPCountry: ipCountry}
}

func saveAccountEvents(t *testing.T, tx pgx.Tx, events ...models.AccountEvent) {
	t.Helper()
	store := NewAccountEventStore(tx)
	for _, e := range events {
		if _, err := store.Save(context.Background(), e); err != nil {
			t.Fatalf("save %s: %v", e.EventID, err)
		}
	}
}

// insertPlayerPurchase saves a purchase of the player with the given
// decision outcome. A review is resolved with resolution unless it is empty,
// which leaves the review open.
func insertPlayerPurchase(t *testing.T, tx pgx.Tx, txnID, playerID string, at time.Time, deviceID, ipCountry *string, outcome, resolution string) {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token, device_id, ip_country)
		VALUES ($1, $2, 1000, $3, 'card-ae', $4, $5)
		RETURNING id`, txnID, at, playerID, deviceID, ipCountry).Scan(&id)
	if err != nil {
		t.Fatalf("insert purchase %s: %v", txnID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs)
		VALUES ($1, 1, $2, 0, '[]', '{}')`, id, outcome); err != nil {
		t.Fatalf("insert decision for %s: %v", txnID, err)
	}
	if resolution == "" {
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_resolutions (purchase_id, decision, analyst_id) VALUES ($1, $2, 'ae-analyst')`, id, resolution); err != nil {
		t.Fatalf("resolve %s: %v", txnID, err)
	}
}

func readAccountHistory(t *testing.T, tx pgx.Tx, playerID string, purchaseAt time.Time) scoring.AccountHistory {
	t.Helper()
	got, err := NewAccountEventStore(tx).History(context.Background(), playerID, purchaseAt.Add(-24*time.Hour), purchaseAt)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	return got
}

func TestSaveAccountEventReportsNewEventsAndRetries(t *testing.T) {
	tx := testTx(t)
	store := NewAccountEventStore(tx)
	e := accountEvent("ae-save-1", "ae-player", models.AccountEventLogin, accountEventBase, ptr("device-1"), ptr("AZ"))

	created, err := store.Save(context.Background(), e)
	if err != nil || !created {
		t.Fatalf("first Save = %v, %v; want true, nil", created, err)
	}
	retried, err := store.Save(context.Background(), e)
	if err != nil || retried {
		t.Fatalf("retry Save = %v, %v; want false, nil", retried, err)
	}
	if n := countRows(t, tx, `SELECT count(*) FROM account_events WHERE event_id = 'ae-save-1'`); n != 1 {
		t.Fatalf("saved %d rows, want 1", n)
	}
}

func TestSaveAccountEventRejectsADifferentEventWithTheSameID(t *testing.T) {
	saved := accountEvent("ae-dup-1", "ae-player", models.AccountEventLogin, accountEventBase, ptr("device-1"), nil)
	tests := []struct {
		name   string
		change func(e *models.AccountEvent)
	}{
		{"another player", func(e *models.AccountEvent) { e.PlayerID = "ae-other" }},
		{"another type", func(e *models.AccountEvent) { e.EventType = models.AccountEventPasswordReset }},
		{"another time", func(e *models.AccountEvent) { e.EventTime = e.EventTime.Add(time.Second) }},
		{"another device", func(e *models.AccountEvent) { e.DeviceID = ptr("device-2") }},
		{"device left out", func(e *models.AccountEvent) { e.DeviceID = nil }},
		{"country added", func(e *models.AccountEvent) { e.IPCountry = ptr("TR") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := testTx(t)
			saveAccountEvents(t, tx, saved)
			changed := saved
			tt.change(&changed)

			_, err := NewAccountEventStore(tx).Save(context.Background(), changed)

			if !errors.Is(err, models.ErrDuplicateAccountEvent) {
				t.Fatalf("err = %v, want ErrDuplicateAccountEvent", err)
			}
		})
	}
}

func TestAccountHistoryReadsOnlyEventsInTheWindowBeforeThePurchase(t *testing.T) {
	tx := testTx(t)
	purchaseAt := accountEventBase
	since := purchaseAt.Add(-24 * time.Hour)
	saveAccountEvents(t, tx,
		accountEvent("ae-w-at-since", "ae-w", models.AccountEventPasswordReset, since, nil, nil),
		accountEvent("ae-w-inside", "ae-w", models.AccountEventPasswordReset, purchaseAt.Add(-time.Hour), nil, nil),
		accountEvent("ae-w-at-purchase", "ae-w", models.AccountEventPasswordReset, purchaseAt, nil, nil),
		accountEvent("ae-w-after", "ae-w", models.AccountEventPasswordReset, purchaseAt.Add(time.Minute), nil, nil),
		accountEvent("ae-w-other-player", "ae-w-other", models.AccountEventPasswordReset, purchaseAt.Add(-time.Minute), nil, nil),
	)

	got := readAccountHistory(t, tx, "ae-w", purchaseAt)

	inside := purchaseAt.Add(-time.Hour)
	if want := (scoring.AccountHistory{LastPasswordReset: &inside}); !reflect.DeepEqual(got, want) {
		t.Errorf("History() = %+v, want %+v", got, want)
	}
}

func TestAccountHistoryKeepsTheLatestPasswordReset(t *testing.T) {
	tx := testTx(t)
	purchaseAt := accountEventBase
	saveAccountEvents(t, tx,
		accountEvent("ae-r-old", "ae-r", models.AccountEventPasswordReset, purchaseAt.Add(-5*time.Hour), nil, nil),
		accountEvent("ae-r-new", "ae-r", models.AccountEventPasswordReset, purchaseAt.Add(-2*time.Hour), nil, nil),
	)

	got := readAccountHistory(t, tx, "ae-r", purchaseAt)

	latest := purchaseAt.Add(-2 * time.Hour)
	if want := (scoring.AccountHistory{LastPasswordReset: &latest}); !reflect.DeepEqual(got, want) {
		t.Errorf("History() = %+v, want %+v", got, want)
	}
}

func TestAccountHistoryListsTheFirstLoginFromANewDeviceAndCountry(t *testing.T) {
	tx := testTx(t)
	purchaseAt := accountEventBase
	at := func(hoursBefore int) time.Time { return purchaseAt.Add(-time.Duration(hoursBefore) * time.Hour) }
	insertPlayerPurchase(t, tx, "ae-n-earlier", "ae-n", at(100), ptr("device-home"), ptr("AZ"), "allow", "")
	saveAccountEvents(t, tx,
		accountEvent("ae-n-reset", "ae-n", models.AccountEventPasswordReset, at(9), ptr("device-new"), ptr("TR")),
		accountEvent("ae-n-known", "ae-n", models.AccountEventLogin, at(8), ptr("device-home"), ptr("AZ")),
		accountEvent("ae-n-new", "ae-n", models.AccountEventLogin, at(6), ptr("device-new"), ptr("TR")),
		accountEvent("ae-n-again", "ae-n", models.AccountEventLogin, at(4), ptr("device-new"), ptr("TR")),
		accountEvent("ae-n-bare", "ae-n", models.AccountEventLogin, at(2), nil, nil),
	)
	// A purchase after a login does not make that login's device known.
	insertPlayerPurchase(t, tx, "ae-n-later", "ae-n", at(5), ptr("device-new"), ptr("TR"), "allow", "")

	got := readAccountHistory(t, tx, "ae-n", purchaseAt)

	reset := at(9)
	want := scoring.AccountHistory{
		LastPasswordReset: &reset,
		NewDeviceLogins:   []time.Time{at(6)},
		NewCountryLogins:  []time.Time{at(6)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("History() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestAccountHistoryKnowsDevicesAndCountriesOnlyFromTrustedPurchases(t *testing.T) {
	tx := testTx(t)
	purchaseAt := accountEventBase
	at := func(hoursBefore int) time.Time { return purchaseAt.Add(-time.Duration(hoursBefore) * time.Hour) }
	insertPlayerPurchase(t, tx, "ae-t-allowed", "ae-t", at(100), ptr("device-allowed"), ptr("AZ"), "allow", "")
	insertPlayerPurchase(t, tx, "ae-t-approved", "ae-t", at(99), ptr("device-approved"), ptr("DE"), "review", "approve")
	insertPlayerPurchase(t, tx, "ae-t-blocked", "ae-t", at(98), ptr("device-blocked"), ptr("US"), "block", "")
	insertPlayerPurchase(t, tx, "ae-t-rejected", "ae-t", at(97), ptr("device-rejected"), ptr("GB"), "review", "reject")
	insertPlayerPurchase(t, tx, "ae-t-open", "ae-t", at(96), ptr("device-open"), ptr("FR"), "review", "")
	saveAccountEvents(t, tx,
		accountEvent("ae-t-1", "ae-t", models.AccountEventLogin, at(10), ptr("device-allowed"), ptr("AZ")),
		accountEvent("ae-t-2", "ae-t", models.AccountEventLogin, at(9), ptr("device-approved"), ptr("DE")),
		accountEvent("ae-t-3", "ae-t", models.AccountEventLogin, at(8), ptr("device-blocked"), ptr("US")),
		accountEvent("ae-t-4", "ae-t", models.AccountEventLogin, at(7), ptr("device-rejected"), ptr("GB")),
		accountEvent("ae-t-5", "ae-t", models.AccountEventLogin, at(6), ptr("device-open"), ptr("FR")),
	)

	got := readAccountHistory(t, tx, "ae-t", purchaseAt)

	untrusted := []time.Time{at(6), at(7), at(8)}
	if want := (scoring.AccountHistory{NewDeviceLogins: untrusted, NewCountryLogins: untrusted}); !reflect.DeepEqual(got, want) {
		t.Errorf("History() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestAccountHistoryNeedsEarlierTrustedActivityToCallALoginNew(t *testing.T) {
	tx := testTx(t)
	purchaseAt := accountEventBase
	insertPlayerPurchase(t, tx, "ae-f-blocked", "ae-f", purchaseAt.Add(-100*time.Hour), ptr("device-0"), ptr("US"), "block", "")
	saveAccountEvents(t, tx,
		accountEvent("ae-f-first", "ae-f", models.AccountEventLogin, purchaseAt.Add(-3*time.Hour), ptr("device-1"), ptr("AZ")),
		accountEvent("ae-f-second", "ae-f", models.AccountEventLogin, purchaseAt.Add(-time.Hour), ptr("device-2"), ptr("AZ")),
	)

	got := readAccountHistory(t, tx, "ae-f", purchaseAt)

	// The first login has nothing trusted to compare with. The second one has
	// the first, which used another device and the same country.
	if want := (scoring.AccountHistory{NewDeviceLogins: []time.Time{purchaseAt.Add(-time.Hour)}}); !reflect.DeepEqual(got, want) {
		t.Errorf("History() = %+v, want %+v", got, want)
	}
}

func TestLaterLoginsDoNotHideAResetOrALoginFromANewDevice(t *testing.T) {
	tx := testTx(t)
	purchaseAt := accountEventBase
	insertPlayerPurchase(t, tx, "ae-h-earlier", "ae-h", purchaseAt.Add(-100*time.Hour), ptr("device-home"), ptr("AZ"), "allow", "")
	saveAccountEvents(t, tx,
		accountEvent("ae-h-reset", "ae-h", models.AccountEventPasswordReset, purchaseAt.Add(-3*time.Hour), nil, nil),
		accountEvent("ae-h-new", "ae-h", models.AccountEventLogin, purchaseAt.Add(-2*time.Hour), ptr("device-new"), ptr("TR")),
	)
	for i := range 100 {
		saveAccountEvents(t, tx, accountEvent(fmt.Sprintf("ae-h-later-%d", i), "ae-h", models.AccountEventLogin,
			purchaseAt.Add(-time.Duration(100-i)*time.Minute), ptr("device-new"), ptr("TR")))
	}
	rs := scoring.Ruleset{ReviewThreshold: 50, BlockThreshold: 1000, Rules: map[scoring.RuleName]scoring.RuleConfig{
		scoring.RuleRecentPasswordReset: {Enabled: true, Weight: 30, Limit: 24},
		scoring.RuleNewDeviceLogin:      {Enabled: true, Weight: 25, Limit: 24},
		scoring.RuleNewCountryLogin:     {Enabled: true, Weight: 20, Limit: 24},
	}}
	history := readAccountHistory(t, tx, "ae-h", purchaseAt)

	got := scoring.Evaluate(scoring.Input{EventTime: purchaseAt, AccountHistory: &history}, rs)

	if len(got.FiredRules) != 3 || got.Score != 75 {
		t.Errorf("fired %+v with score %d, want all three account rules", got.FiredRules, got.Score)
	}
}

func TestAccountHistoryChecksAtMostTheNewestFirstLogins(t *testing.T) {
	tx := testTx(t)
	purchaseAt := accountEventBase
	insertPlayerPurchase(t, tx, "ae-m-earlier", "ae-m", purchaseAt.Add(-100*time.Hour), ptr("device-home"), ptr("AZ"), "allow", "")
	for i := range maxNewLoginCandidates + 5 {
		saveAccountEvents(t, tx, accountEvent(fmt.Sprintf("ae-m-%d", i), "ae-m", models.AccountEventLogin,
			purchaseAt.Add(-time.Duration(i+1)*time.Minute), ptr(fmt.Sprintf("device-%d", i)), nil))
	}

	got := readAccountHistory(t, tx, "ae-m", purchaseAt)

	logins := got.NewDeviceLogins
	if len(logins) != maxNewLoginCandidates || !logins[0].Equal(purchaseAt.Add(-time.Minute)) ||
		!logins[len(logins)-1].Equal(purchaseAt.Add(-time.Duration(maxNewLoginCandidates)*time.Minute)) {
		t.Errorf("checked %d first logins from %v, want the newest %d", len(logins), logins, maxNewLoginCandidates)
	}
}

func TestCaseListsTheRecentAccountEventsOfThePlayer(t *testing.T) {
	tx := testTx(t)
	id := insertReviewDecision(t, tx, "c-events", "review", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil, `[]`)
	purchaseAt := time.Date(2000, 1, 1, 8, 0, 0, 0, time.UTC)
	player := "player-c-events"
	saveAccountEvents(t, tx,
		accountEvent("c-ev-31-days", player, models.AccountEventLogin, purchaseAt.AddDate(0, 0, -31), nil, nil),
		accountEvent("c-ev-after", player, models.AccountEventLogin, purchaseAt.Add(time.Minute), nil, nil),
		accountEvent("c-ev-other", "player-other", models.AccountEventLogin, purchaseAt.Add(-time.Minute), nil, nil),
		accountEvent("c-ev-reset", player, models.AccountEventPasswordReset, purchaseAt.Add(-2*time.Hour), ptr("device-x"), ptr("TR")),
	)
	for i := range caseAccountEventLimit {
		saveAccountEvents(t, tx, accountEvent(fmt.Sprintf("c-ev-login-%02d", i), player, models.AccountEventLogin,
			purchaseAt.Add(-time.Duration(i+1)*time.Minute), nil, nil))
	}

	got, err := NewReviewStore(tx).Case(context.Background(), id)

	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	events := got.AccountEvents
	if len(events) != caseAccountEventLimit || events[0].EventID != "c-ev-login-00" || events[len(events)-1].EventID != "c-ev-login-19" {
		t.Fatalf("account events = %+v, want the %d newest logins", events, caseAccountEventLimit)
	}
	if events[0].PlayerID != player || events[0].EventType != models.AccountEventLogin || !events[0].EventTime.Equal(purchaseAt.Add(-time.Minute)) {
		t.Errorf("newest event = %+v", events[0])
	}
}

func TestCaseShowsAPasswordResetWithItsDeviceAndCountry(t *testing.T) {
	tx := testTx(t)
	id := insertReviewDecision(t, tx, "c-reset", "review", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil, `[]`)
	purchaseAt := time.Date(2000, 1, 1, 8, 0, 0, 0, time.UTC)
	reset := accountEvent("c-reset-1", "player-c-reset", models.AccountEventPasswordReset, purchaseAt.Add(-2*time.Hour), ptr("device-x"), ptr("TR"))
	saveAccountEvents(t, tx, reset)

	got, err := NewReviewStore(tx).Case(context.Background(), id)

	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if len(got.AccountEvents) != 1 {
		t.Fatalf("account events = %+v, want the reset", got.AccountEvents)
	}
	event := got.AccountEvents[0]
	if event.ReceivedAt.IsZero() || event.ReceivedAt.Location() != time.UTC {
		t.Errorf("received at %v, want the recording time in UTC", event.ReceivedAt)
	}
	event.ReceivedAt = time.Time{}
	if !reflect.DeepEqual(event, reset) {
		t.Errorf("account event = %+v, want %+v", event, reset)
	}
}
