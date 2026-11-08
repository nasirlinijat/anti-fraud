package reviews

import (
	"context"
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
		INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, model_score, decided_at)
		VALUES ($1, 1, $2, 60, $3, '{}', $4, $5)`, id, outcome, firedRules, modelScore, decidedAt)
	if err != nil {
		t.Fatalf("insert decision for %s: %v", txnID, err)
	}
	return id
}

func TestQueueReturnsReviewsOldestFirst(t *testing.T) {
	tx := testTx(t)
	at := func(minute int) time.Time { return time.Date(2000, 1, 1, 9, minute, 0, 0, time.UTC) }
	score := 0.7

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
