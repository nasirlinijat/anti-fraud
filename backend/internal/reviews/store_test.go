package reviews

import (
	"context"
	"errors"
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

// insertDecision adds a purchase and its decision. The decision times are in
// 2000 so the test rows come before any other rows in the queue.
func insertDecision(t *testing.T, tx pgx.Tx, txnID, outcome string, decidedAt time.Time, modelScore *float64, firedRules string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token)
		VALUES ($1, '2000-01-01 08:00:00+00', 2500, 'player-' || $1, 'card-1')
		RETURNING id`, txnID).Scan(&id)
	if err != nil {
		t.Fatalf("insert purchase %s: %v", txnID, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, model_score, model_version, decided_at)
		VALUES ($1, 1, $2, 60, $3, '{}', $4, CASE WHEN $4::float8 IS NULL THEN NULL ELSE $6 END, $5)`,
		id, outcome, firedRules, modelScore, decidedAt, testModelVersion)
	if err != nil {
		t.Fatalf("insert decision for %s: %v", txnID, err)
	}
	return id
}

const testModelVersion = "lgbm-test"

func TestQueueReturnsReviewsOldestFirst(t *testing.T) {
	tx := testTx(t)
	at := func(minute int) time.Time { return time.Date(2000, 1, 1, 9, minute, 0, 0, time.UTC) }
	score := 0.7
	version := testModelVersion

	later := insertDecision(t, tx, "q-later", "review", at(30), nil, `[]`)
	insertDecision(t, tx, "q-allow", "allow", at(5), nil, `[]`)
	insertDecision(t, tx, "q-block", "block", at(6), nil, `[]`)
	earlier := insertDecision(t, tx, "q-earlier", "review", at(10), &score,
		`[{"name": "high_amount", "points": 30}, {"name": "card_velocity", "points": 40}]`)

	got, err := NewStore(tx).Queue(context.Background(), 2)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}

	want := []QueueItem{
		{
			PurchaseID:    earlier,
			TransactionID: "q-earlier",
			EventTime:     time.Date(2000, 1, 1, 8, 0, 0, 0, time.UTC),
			AmountMinor:   2500,
			PlayerID:      "player-q-earlier",
			RuleScore:     60,
			ModelScore:    &score,
			ModelVersion:  &version,
			FiredRules: []scoring.FiredRule{
				{Name: scoring.RuleHighAmount, Points: 30, Message: "high_amount"},
				{Name: scoring.RuleCardVelocity, Points: 40, Message: "card_velocity"},
			},
			DecidedAt: at(10),
		},
		{
			PurchaseID:    later,
			TransactionID: "q-later",
			EventTime:     time.Date(2000, 1, 1, 8, 0, 0, 0, time.UTC),
			AmountMinor:   2500,
			PlayerID:      "player-q-later",
			RuleScore:     60,
			FiredRules:    []scoring.FiredRule{},
			DecidedAt:     at(30),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Queue() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestQueueRespectsLimit(t *testing.T) {
	tx := testTx(t)
	for i, id := range []string{"l-1", "l-2", "l-3"} {
		insertDecision(t, tx, id, "review", time.Date(2000, 1, 1, 9, i, 0, 0, time.UTC), nil, `[]`)
	}

	got, err := NewStore(tx).Queue(context.Background(), 1)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(got) != 1 || got[0].TransactionID != "l-1" {
		t.Errorf("Queue(1) = %+v, want only l-1", got)
	}
}

func TestCaseReturnsEvidenceOfReviewPurchase(t *testing.T) {
	tx := testTx(t)
	at := time.Date(2000, 1, 1, 9, 10, 0, 0, time.UTC)
	score := 0.7
	version := testModelVersion
	id := insertDecision(t, tx, "c-review", "review", at, &score,
		`[{"name": "high_amount", "points": 30}, {"name": "card_velocity", "points": 40}]`)

	got, err := NewStore(tx).Case(context.Background(), id)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	want := CaseItem{
		QueueItem: QueueItem{
			PurchaseID:    id,
			TransactionID: "c-review",
			EventTime:     time.Date(2000, 1, 1, 8, 0, 0, 0, time.UTC),
			AmountMinor:   2500,
			PlayerID:      "player-c-review",
			RuleScore:     60,
			ModelScore:    &score,
			ModelVersion:  &version,
			FiredRules: []scoring.FiredRule{
				{Name: scoring.RuleHighAmount, Points: 30, Message: "high_amount"},
				{Name: scoring.RuleCardVelocity, Points: 40, Message: "card_velocity"},
			},
			DecidedAt: at,
		},
		RulesetVersion:  1,
		PlayerPurchases: []PlayerPurchase{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Case() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestCaseWithoutModelScore(t *testing.T) {
	tx := testTx(t)
	id := insertDecision(t, tx, "c-no-model", "review", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil, `[]`)

	got, err := NewStore(tx).Case(context.Background(), id)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if got.ModelScore != nil || got.ModelVersion != nil || got.FiredRules == nil || len(got.FiredRules) != 0 {
		t.Errorf("Case() = %+v, want no model score or version and an empty rule list", got)
	}
}

func TestQueueReturnsDeviceID(t *testing.T) {
	tx := testTx(t)
	withDevice := insertDecision(t, tx, "qd-device", "review", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil, `[]`)
	withoutDevice := insertDecision(t, tx, "qd-none", "review", time.Date(2000, 1, 1, 9, 1, 0, 0, time.UTC), nil, `[]`)
	if _, err := tx.Exec(context.Background(), `UPDATE purchases SET device_id = 'device-q' WHERE id = $1`, withDevice); err != nil {
		t.Fatalf("set device ID: %v", err)
	}

	got, err := NewStore(tx).Queue(context.Background(), 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("Queue = %+v, %v; want 2 items", got, err)
	}
	if got[0].PurchaseID != withDevice || got[0].DeviceID == nil || *got[0].DeviceID != "device-q" {
		t.Errorf("first item device = %v, want device-q", got[0].DeviceID)
	}
	if got[1].PurchaseID != withoutDevice || got[1].DeviceID != nil {
		t.Errorf("second item device = %v, want nil", got[1].DeviceID)
	}
}

func TestCaseReturnsDeviceID(t *testing.T) {
	tx := testTx(t)
	withDevice := insertDecision(t, tx, "c-device", "review", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil, `[]`)
	withoutDevice := insertDecision(t, tx, "c-no-device", "review", time.Date(2000, 1, 1, 9, 1, 0, 0, time.UTC), nil, `[]`)
	if _, err := tx.Exec(context.Background(), `UPDATE purchases SET device_id = 'device-7' WHERE id = $1`, withDevice); err != nil {
		t.Fatalf("set device ID: %v", err)
	}

	store := NewStore(tx)
	got, err := store.Case(context.Background(), withDevice)
	if err != nil || got.DeviceID == nil || *got.DeviceID != "device-7" {
		t.Errorf("Case(with device) = %+v, %v; want device-7", got.DeviceID, err)
	}
	got, err = store.Case(context.Background(), withoutDevice)
	if err != nil || got.DeviceID != nil {
		t.Errorf("Case(without device) = %+v, %v; want nil", got.DeviceID, err)
	}
}

func TestCaseNotFound(t *testing.T) {
	tx := testTx(t)
	allowed := insertDecision(t, tx, "c-allow", "allow", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil, `[]`)
	blocked := insertDecision(t, tx, "c-block", "block", time.Date(2000, 1, 1, 9, 1, 0, 0, time.UTC), nil, `[]`)

	store := NewStore(tx)
	for name, id := range map[string]int64{"allowed purchase": allowed, "blocked purchase": blocked, "unknown ID": allowed + blocked + 1_000_000} {
		if _, err := store.Case(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: Case error = %v, want ErrNotFound", name, err)
		}
	}
}

func TestQueueAndCaseReturnTheSavedRuleMessage(t *testing.T) {
	tx := testTx(t)
	id := insertDecision(t, tx, "m-msg", "review", time.Date(2000, 1, 1, 9, 0, 0, 0, time.UTC), nil,
		`[{"name": "card_velocity", "points": 40, "message": "Card used 5 times in 60 minutes (limit 5)"}, {"name": "high_amount", "points": 30}]`)
	want := []scoring.FiredRule{
		{Name: scoring.RuleCardVelocity, Points: 40, Message: "Card used 5 times in 60 minutes (limit 5)"},
		{Name: scoring.RuleHighAmount, Points: 30, Message: "high_amount"}, // saved before messages existed
	}

	store := NewStore(tx)
	queue, err := store.Queue(context.Background(), 1)
	if err != nil || len(queue) != 1 || !reflect.DeepEqual(queue[0].FiredRules, want) {
		t.Errorf("Queue fired rules = %+v, %v; want %+v", queue, err, want)
	}
	reviewCase, err := store.Case(context.Background(), id)
	if err != nil || !reflect.DeepEqual(reviewCase.FiredRules, want) {
		t.Errorf("Case fired rules = %+v, %v; want %+v", reviewCase.FiredRules, err, want)
	}
}

func TestCaseReturnsPlayerAndDeviceHistory(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	base := time.Date(2000, 1, 2, 12, 0, 0, 0, time.UTC)

	insertHistoryRow := func(txnID, playerID, cardToken string, deviceID *string, eventTime time.Time, amount int64, outcome string) int64 {
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token, device_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`, txnID, eventTime, amount, playerID, cardToken, deviceID).Scan(&id)
		if err != nil {
			t.Fatalf("insert purchase %s: %v", txnID, err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, decided_at)
			VALUES ($1, 1, $2, 60, '[]', '{}', $3)`, id, outcome, eventTime)
		if err != nil {
			t.Fatalf("insert decision %s: %v", txnID, err)
		}
		return id
	}

	device := "device-shared"
	otherDevice := "device-other"
	for i := range 6 {
		insertHistoryRow("hist-p-"+string(rune('1'+i)), "player-hist", "card-1", &otherDevice,
			base.Add(time.Duration(i)*time.Minute), int64(1000*(i+1)), "allow")
	}
	insertHistoryRow("hist-other-player", "player-other", "card-2", &device, base.Add(3*time.Minute), 4000, "block")
	targetID := insertHistoryRow("hist-target", "player-hist", "card-3", &device, base.Add(10*time.Minute), 9000, "review")
	// Purchases after the target must not appear in the target's history.
	insertHistoryRow("hist-after", "player-hist", "card-4", &device, base.Add(20*time.Minute), 9999, "allow")

	got, err := NewStore(tx).Case(ctx, targetID)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	wantPurchases := []PlayerPurchase{
		{TransactionID: "hist-p-6", EventTime: base.Add(5 * time.Minute), AmountMinor: 6000, Outcome: scoring.OutcomeAllow},
		{TransactionID: "hist-p-5", EventTime: base.Add(4 * time.Minute), AmountMinor: 5000, Outcome: scoring.OutcomeAllow},
		{TransactionID: "hist-p-4", EventTime: base.Add(3 * time.Minute), AmountMinor: 4000, Outcome: scoring.OutcomeAllow},
		{TransactionID: "hist-p-3", EventTime: base.Add(2 * time.Minute), AmountMinor: 3000, Outcome: scoring.OutcomeAllow},
		{TransactionID: "hist-p-2", EventTime: base.Add(1 * time.Minute), AmountMinor: 2000, Outcome: scoring.OutcomeAllow},
	}
	if !reflect.DeepEqual(got.PlayerPurchases, wantPurchases) {
		t.Errorf("PlayerPurchases =\n%+v\nwant\n%+v", got.PlayerPurchases, wantPurchases)
	}
	wantDevice := &DeviceActivity{
		Purchases:     2,
		DistinctCards: 2,
		LastSeenAt:    base.Add(10 * time.Minute),
	}
	if !reflect.DeepEqual(got.DeviceActivity, wantDevice) {
		t.Errorf("DeviceActivity = %+v, want %+v", got.DeviceActivity, wantDevice)
	}
}
