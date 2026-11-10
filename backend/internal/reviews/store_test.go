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
				{Name: scoring.RuleHighAmount, Points: 30},
				{Name: scoring.RuleCardVelocity, Points: 40},
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
				{Name: scoring.RuleHighAmount, Points: 30},
				{Name: scoring.RuleCardVelocity, Points: 40},
			},
			DecidedAt: at,
		},
		RulesetVersion: 1,
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
