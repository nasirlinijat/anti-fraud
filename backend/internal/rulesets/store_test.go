package rulesets

import (
	"context"
	"os"
	"reflect"
	"testing"

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

func TestActiveDefault(t *testing.T) {
	pool := testPool(t)

	got, err := NewStore(pool).Active(context.Background())
	if err != nil {
		t.Fatalf("Active: %v", err)
	}

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

func TestActiveAfterSwitch(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx)

	statements := []string{
		`UPDATE rulesets SET is_active = false WHERE is_active`,
		`INSERT INTO rulesets (version, review_threshold, block_threshold, is_active) VALUES (99, 40, 70, true)`,
		`INSERT INTO ruleset_rules (ruleset_version, rule_name, enabled, weight, rule_limit) VALUES (99, 'high_amount', false, 10, 1000)`,
	}
	for _, s := range statements {
		if _, err := tx.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}

	got, err := NewStore(tx).Active(ctx)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}

	want := scoring.Ruleset{
		Version:         99,
		ReviewThreshold: 40,
		BlockThreshold:  70,
		Rules: map[scoring.RuleName]scoring.RuleConfig{
			scoring.RuleHighAmount: {Enabled: false, Weight: 10, Limit: 1000},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Active() = %+v, want %+v", got, want)
	}
}
