package main

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

func TestRuntimeRolePermissions(t *testing.T) {
	dsn := os.Getenv("TEST_RUNTIME_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_RUNTIME_DATABASE_URL is not set")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	for _, statement := range []string{
		"CREATE TABLE public.runtime_role_should_not_create (id int)",
		"SELECT version FROM schema_migrations",
		"SELECT is_fraud FROM purchase_labels",
		"SELECT is_fraud FROM pending_purchase_labels",
		"SELECT actor_id FROM configuration_audit_events",
		"UPDATE configuration_audit_events SET actor_id = 'changed'",
		"DELETE FROM configuration_audit_events",
	} {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, statement)
		_ = tx.Rollback(ctx)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("%s: expected insufficient privilege, got %v", statement, err)
		}
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	rules := storage.NewRulesetStore(tx)
	active, err := rules.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Publish(ctx, scoring.Ruleset{
		ReviewThreshold: active.ReviewThreshold,
		BlockThreshold:  active.BlockThreshold,
		Rules:           active.Rules,
	}, "test-operator", nil); err != nil {
		t.Fatalf("publish with restricted role: %v", err)
	}
	blocks := storage.NewBlocklistStore(tx)
	entry, err := blocks.Add(ctx, models.BlocklistEntry{
		Type: models.BlocklistDevice, Value: "runtime-role-test", Reason: "permission check",
	}, "test-operator")
	if err != nil {
		t.Fatalf("add with restricted role: %v", err)
	}
	if err := blocks.Remove(ctx, entry.ID, "test-operator"); err != nil {
		t.Fatalf("remove with restricted role: %v", err)
	}
}
