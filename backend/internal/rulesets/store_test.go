package rulesets

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func testPool(t *testing.T) *pgxpool.Pool {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// txWith runs statements in a transaction that is rolled back when the test ends.
func txWith(t *testing.T, pool *pgxpool.Pool, statements ...string) pgx.Tx {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(ctx) })

	for _, s := range statements {
		if _, err := tx.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return tx
}

func TestActiveDefault(t *testing.T) {
	pool := testPool(t)

	got, err := NewStore(pool).Active(context.Background())
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if got.CreatedAt.IsZero() {
		t.Error("Active().CreatedAt is zero")
	}
	got.CreatedAt = wantZeroTime

	want := scoring.Ruleset{
		Version:         1,
		ReviewThreshold: 50,
		BlockThreshold:  80,
		Rules: map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleHighAmount:      {Enabled: true, Weight: 30, Limit: 50000},
			scoring.RuleCardVelocity:    {Enabled: true, Weight: 40, Limit: 5},
			scoring.RuleNewAccount:      {Enabled: true, Weight: 20, Limit: 24},
			scoring.RuleCountryMismatch: {Enabled: true, Weight: 25},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Active() = %+v, want %+v", got, want)
	}
}

var wantZeroTime time.Time

func TestActiveAfterSwitch(t *testing.T) {
	tx := txWith(t, testPool(t),
		`UPDATE rulesets SET is_active = false WHERE is_active`,
		`INSERT INTO rulesets (version, review_threshold, block_threshold, note, is_active) VALUES (99, 40, 70, 'Lower thresholds', true)`,
		`INSERT INTO ruleset_rules (ruleset_version, rule_name, enabled, weight, rule_limit) VALUES (99, 'high_amount', false, 10, 1000)`,
	)

	got, err := NewStore(tx).Active(context.Background())
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	got.CreatedAt = wantZeroTime

	want := scoring.Ruleset{
		Version:         99,
		ReviewThreshold: 40,
		BlockThreshold:  70,
		Note:            "Lower thresholds",
		Rules: map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleHighAmount: {Enabled: false, Weight: 10, Limit: 1000},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Active() = %+v, want %+v", got, want)
	}
}

func TestRulesetVersionsCannotChange(t *testing.T) {
	pool := testPool(t)
	statements := []string{
		`UPDATE ruleset_rules SET weight = 90 WHERE ruleset_version = 1`,
		`DELETE FROM ruleset_rules WHERE ruleset_version = 1`,
		`UPDATE rulesets SET block_threshold = 90 WHERE version = 1`,
		`UPDATE rulesets SET note = 'changed' WHERE version = 1`,
		`DELETE FROM rulesets WHERE version = 1`,
	}

	for _, s := range statements {
		t.Run(s, func(t *testing.T) {
			tx := txWith(t, pool)
			_, err := tx.Exec(context.Background(), s)
			if err == nil || !strings.Contains(err.Error(), "cannot be changed") {
				t.Errorf("error = %v, want the change to be rejected", err)
			}
		})
	}
}

func TestActiveUnknownRule(t *testing.T) {
	tx := txWith(t, testPool(t),
		`UPDATE rulesets SET is_active = false WHERE is_active`,
		`INSERT INTO rulesets (version, review_threshold, block_threshold, is_active) VALUES (98, 40, 70, true)`,
		`INSERT INTO ruleset_rules (ruleset_version, rule_name, enabled, weight, rule_limit) VALUES (98, 'high_ammount', true, 10, 1000)`,
	)

	_, err := NewStore(tx).Active(context.Background())
	if err == nil || !strings.Contains(err.Error(), "high_ammount") {
		t.Errorf("Active() error = %v, want an error naming high_ammount", err)
	}
}

func TestListAndVersionHistory(t *testing.T) {
	tx := txWith(t, testPool(t),
		`UPDATE rulesets SET is_active = false WHERE is_active`,
		`INSERT INTO rulesets (version, review_threshold, block_threshold, note, is_active, created_at)
		 VALUES (50, 45, 75, 'Tune high amount', false, '2026-11-10T08:00:00Z'),
		        (51, 40, 70, 'Lower thresholds', true, '2026-11-10T09:00:00Z')`,
		`INSERT INTO ruleset_rules (ruleset_version, rule_name, enabled, weight, rule_limit)
		 VALUES (50, 'high_amount', true, 35, 60000),
		        (51, 'high_amount', true, 25, 40000)`,
	)
	store := NewStore(tx)
	ctx := context.Background()

	list, err := store.List(ctx)
	if err != nil || len(list) < 3 {
		t.Fatalf("List() = %+v, %v; want at least 3 versions", list, err)
	}
	if list[0].Version != 51 || list[0].Note != "Lower thresholds" ||
		list[1].Version != 50 || list[1].Note != "Tune high amount" {
		t.Errorf("newest versions = %+v, want 51 then 50", list[:2])
	}

	v50, err := store.Version(ctx, 50)
	if err != nil {
		t.Fatalf("Version(50): %v", err)
	}
	want50 := scoring.Ruleset{
		Version:         50,
		ReviewThreshold: 45,
		BlockThreshold:  75,
		Note:            "Tune high amount",
		CreatedAt:       time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC),
		Rules: map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleHighAmount: {Enabled: true, Weight: 35, Limit: 60000},
		},
	}
	if !reflect.DeepEqual(v50, want50) {
		t.Errorf("Version(50) = %+v, want %+v", v50, want50)
	}

	if _, err := store.Version(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Version(999999) error = %v, want ErrNotFound", err)
	}
}
