package simulation

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"go.uber.org/mock/gomock"
)

func TestBlockedPurchasesNamesTheReason(t *testing.T) {
	s, store, _ := newMockService(t)
	store.EXPECT().Get(gomock.Any(), "sim-1").Return(models.SimulationRun{ID: "sim-1"}, nil)
	store.EXPECT().BlockedPurchases(gomock.Any(), "sim-1", int64(0), 3).Return([]models.BlockedPurchase{
		{PurchaseID: 9, FiredRules: []scoring.FiredRule{scoring.BlocklistMatch("card")}},
		{PurchaseID: 7, FiredRules: []scoring.FiredRule{{Name: "velocity", Points: 90}}},
	}, nil)

	items, next, err := s.BlockedPurchases(context.Background(), "sim-1", 0, 2)
	if err != nil || next != 0 || len(items) != 2 {
		t.Fatalf("BlockedPurchases = %d items, next %d, %v; want 2 items on the last page", len(items), next, err)
	}
	if items[0].Reason != models.BlockReasonBlocklist || items[1].Reason != models.BlockReasonRules {
		t.Fatalf("reasons = %q, %q; want blocklist, rules", items[0].Reason, items[1].Reason)
	}
}

func TestBlockedPurchasesPages(t *testing.T) {
	s, store, _ := newMockService(t)
	store.EXPECT().Get(gomock.Any(), "sim-1").Return(models.SimulationRun{ID: "sim-1"}, nil)
	store.EXPECT().BlockedPurchases(gomock.Any(), "sim-1", int64(20), 3).Return([]models.BlockedPurchase{
		{PurchaseID: 15}, {PurchaseID: 12}, {PurchaseID: 4},
	}, nil)

	items, next, err := s.BlockedPurchases(context.Background(), "sim-1", 20, 2)
	if err != nil || len(items) != 2 || next != 12 {
		t.Fatalf("BlockedPurchases = %d items, next %d, %v; want 2 items and next 12", len(items), next, err)
	}
}

func TestBlockedPurchasesOfAnUnknownRun(t *testing.T) {
	s, store, _ := newMockService(t)
	store.EXPECT().Get(gomock.Any(), "sim-x").Return(models.SimulationRun{}, models.ErrSimulationNotFound)
	store.EXPECT().BlockedPurchases(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	if _, _, err := s.BlockedPurchases(context.Background(), "sim-x", 0, 20); !errors.Is(err, models.ErrSimulationNotFound) {
		t.Fatalf("err = %v, want ErrSimulationNotFound", err)
	}
}

func TestBlockedPurchasesReadsOnlyTheRunsBlocks(t *testing.T) {
	s, store, pool := setup(t, nil)
	run := createRun(t, store)
	addDecision(t, pool, run.ID+"-txn-1", "block", `[{"name":"blocklist","points":0,"message":"Blocked: the card is on the blocklist"}]`)
	addDecision(t, pool, run.ID+"-txn-2", "allow", `[]`)
	addDecision(t, pool, run.ID+"-txn-10", "block", `[{"name":"velocity","points":95,"message":"fast"}]`)
	addDecision(t, pool, run.ID+"x-txn-3", "block", `[]`)
	addDecision(t, pool, "sim-other-txn-4", "block", `[]`)

	first, next, err := s.BlockedPurchases(t.Context(), run.ID, 0, 1)
	if err != nil || len(first) != 1 || next == 0 {
		t.Fatalf("first page = %d items, next %d, %v; want 1 item and a cursor", len(first), next, err)
	}
	second, last, err := s.BlockedPurchases(t.Context(), run.ID, next, 1)
	if err != nil || len(second) != 1 || last != 0 {
		t.Fatalf("second page = %d items, next %d, %v; want the last item", len(second), last, err)
	}
	newest, oldest := first[0], second[0]
	if newest.TransactionID != run.ID+"-txn-10" || newest.Reason != models.BlockReasonRules || newest.FiredRules[0].Points != 95 {
		t.Fatalf("newest = %+v, want the rule block", newest)
	}
	if oldest.TransactionID != run.ID+"-txn-1" || oldest.Reason != models.BlockReasonBlocklist || oldest.BlockThreshold == 0 {
		t.Fatalf("oldest = %+v, want the blocklist block with its threshold", oldest)
	}
}

// createRun saves a finished run without sending traffic.
func createRun(t *testing.T, store *storage.SimulationStore) models.SimulationRun {
	t.Helper()
	c, ok, err := store.Lock(t.Context())
	if err != nil || !ok {
		t.Fatal("lock", ok, err)
	}
	defer c.Close()
	run, err := c.Create(t.Context(), "sim-blocked", "op", settings(t, `{"request_id":"blocked","mode":"live","rate":5}`))
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// addDecision saves a purchase and its decision under the active ruleset.
func addDecision(t *testing.T, pool *pgxpool.Pool, transactionID, outcome, firedRules string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
WITH p AS (
  INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token)
  VALUES ($1, now(), 1999, 'player', 'card') RETURNING id
)
INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs)
SELECT p.id, (SELECT version FROM rulesets WHERE is_active), $2, 0, $3::jsonb, '{}'::jsonb FROM p`,
		transactionID, outcome, firedRules)
	if err != nil {
		t.Fatal(err)
	}
}
