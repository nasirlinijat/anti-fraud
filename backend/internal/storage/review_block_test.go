package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

// insertBlockablePurchase adds a scored purchase whose card is
// "<txnID>-card" and whose player is "<txnID>-player". Its device is the given
// one, or none when device is nil.
func insertBlockablePurchase(t *testing.T, tx pgx.Tx, txnID, outcome string, device *string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token, device_id)
		VALUES ($1, '2000-01-01 08:00:00+00', 5000, $1 || '-player', $1 || '-card', $2)
		RETURNING id`, txnID, device).Scan(&id)
	if err != nil {
		t.Fatalf("insert purchase %s: %v", txnID, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, decided_at)
		VALUES ($1, 1, $2, 60, '[]', '{}', now())`, id, outcome)
	if err != nil {
		t.Fatalf("insert decision for %s: %v", txnID, err)
	}
	return id
}

// blockRow is a blocklist entry with the analyst its audit event names.
type blockRow struct {
	Entry   string
	Reason  string
	Analyst string
}

// blocklistRows returns the entries for the values in the order they were
// added, as "type:value".
func blocklistRows(t *testing.T, tx pgx.Tx, values ...string) []blockRow {
	t.Helper()
	rows, err := tx.Query(context.Background(), `
		SELECT b.entry_type || ':' || b.value, b.reason, a.actor_id
		FROM blocklist_entries b
		LEFT JOIN configuration_audit_events a ON a.resource_id = b.id AND a.action = 'blocklist.add'
		WHERE b.value = ANY($1)
		ORDER BY b.id`, values)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (blockRow, error) {
		var r blockRow
		var analyst *string
		err := row.Scan(&r.Entry, &r.Reason, &analyst)
		if analyst != nil {
			r.Analyst = *analyst
		}
		return r, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func rejectionRows(purchaseID int64, entries ...string) []blockRow {
	rows := make([]blockRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, blockRow{
			Entry:   entry,
			Reason:  fmt.Sprintf("Blocked when analyst-1 rejected purchase %d", purchaseID),
			Analyst: "analyst-1",
		})
	}
	return rows
}

func TestResolveBlocksTheRequestedValuesOfARejectedPurchase(t *testing.T) {
	tests := []struct {
		name        string
		txnID       string
		blocks      models.ReviewBlocks
		wantBlocked models.ReviewBlocks
		wantEntries []string
	}{
		{"device", "rb-device-only", models.ReviewBlocks{Device: true}, models.ReviewBlocks{Device: true},
			[]string{"device:rb-device-only-device"}},
		{"card", "rb-card-only", models.ReviewBlocks{Card: true}, models.ReviewBlocks{Card: true},
			[]string{"card:rb-card-only-card"}},
		{"player", "rb-player-only", models.ReviewBlocks{Player: true}, models.ReviewBlocks{Player: true},
			[]string{"player:rb-player-only-player"}},
		{"all three", "rb-all", models.ReviewBlocks{Device: true, Card: true, Player: true}, models.ReviewBlocks{Device: true, Card: true, Player: true},
			[]string{"device:rb-all-device", "card:rb-all-card", "player:rb-all-player"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := testTx(t)
			device := tt.txnID + "-device"
			id := insertBlockablePurchase(t, tx, tt.txnID, "review", &device)
			refresher := &spyRefresher{}

			claimReviewFixture(t, tx, id, "analyst-1")
			res, err := NewReviewStore(tx).UseBlocklistCache(refresher).Resolve(context.Background(), models.ReviewDecision{
				PurchaseID: id, Decision: "reject", AnalystID: "analyst-1", Blocks: tt.blocks,
			})

			if err != nil || res.Status != models.ResolutionSaved || res.Blocked != tt.wantBlocked {
				t.Fatalf("Resolve = %+v, %v; want saved with %+v blocked", res, err, tt.wantBlocked)
			}
			got := blocklistRows(t, tx, device, tt.txnID+"-card", tt.txnID+"-player")
			if want := rejectionRows(id, tt.wantEntries...); !reflect.DeepEqual(got, want) {
				t.Errorf("entries = %+v, want %+v", got, want)
			}
			if !reflect.DeepEqual(refresher.refreshed, tt.wantEntries) {
				t.Errorf("refreshed = %v, want %v", refresher.refreshed, tt.wantEntries)
			}
		})
	}
}

func TestResolveLeavesValuesThatAreAlreadyBlocked(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	blocklist := NewBlocklistStore(tx)
	for _, e := range []models.BlocklistEntry{
		{Type: models.BlocklistCard, Value: "rb-known-card", Reason: "earlier"},
		{Type: models.BlocklistPlayer, Value: "rb-known-player", Reason: "earlier"},
	} {
		if _, err := blocklist.Add(ctx, e, "operator-1"); err != nil {
			t.Fatal(err)
		}
	}
	device := "rb-known-device"
	id := insertBlockablePurchase(t, tx, "rb-known", "review", &device)
	refresher := &spyRefresher{}

	claimReviewFixture(t, tx, id, "analyst-1")
	res, err := NewReviewStore(tx).UseBlocklistCache(refresher).Resolve(ctx, models.ReviewDecision{
		PurchaseID: id, Decision: "reject", AnalystID: "analyst-1", Blocks: models.ReviewBlocks{Device: true, Card: true, Player: true},
	})

	if err != nil || res.Status != models.ResolutionSaved || res.Blocked != (models.ReviewBlocks{Device: true}) {
		t.Fatalf("Resolve = %+v, %v; want saved with only the device blocked", res, err)
	}
	want := []blockRow{
		{Entry: "card:rb-known-card", Reason: "earlier", Analyst: "operator-1"},
		{Entry: "player:rb-known-player", Reason: "earlier", Analyst: "operator-1"},
	}
	want = append(want, rejectionRows(id, "device:rb-known-device")...)
	if got := blocklistRows(t, tx, "rb-known-device", "rb-known-card", "rb-known-player"); !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %+v, want %+v", got, want)
	}
	if want := []string{"device:rb-known-device"}; !reflect.DeepEqual(refresher.refreshed, want) {
		t.Errorf("refreshed = %v, want %v", refresher.refreshed, want)
	}
}

func TestResolveAddsNoEntriesWithoutASavedRejection(t *testing.T) {
	every := models.ReviewBlocks{Device: true, Card: true, Player: true}
	stranger := "rb-stranger"
	tests := []struct {
		name         string
		txnID        string
		outcome      string
		decision     models.ReviewDecision
		decidedFirst bool
		wantStatus   models.ResolutionStatus
	}{
		{"approve", "rb-approve", "review",
			models.ReviewDecision{Decision: "approve", AnalystID: "analyst-1", Blocks: every}, false, models.ResolutionSaved},
		{"session without the claim", "rb-unclaimed", "review",
			models.ReviewDecision{Decision: "reject", AnalystID: stranger, Blocks: every}, false, models.ResolutionNotClaimed},
		{"already decided", "rb-decided", "review",
			models.ReviewDecision{Decision: "reject", AnalystID: "analyst-1", Blocks: every}, true, models.ResolutionAlreadyResolved},
		{"not under review", "rb-allowed", "allow",
			models.ReviewDecision{Decision: "reject", AnalystID: "analyst-1", Blocks: every}, false, models.ResolutionNotReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := testTx(t)
			ctx := context.Background()
			device := tt.txnID + "-device"
			id := insertBlockablePurchase(t, tx, tt.txnID, tt.outcome, &device)
			store := NewReviewStore(tx)
			if tt.decidedFirst {
				claimReviewFixture(t, tx, id, "analyst-2")
				if _, err := store.Resolve(ctx, models.ReviewDecision{PurchaseID: id, Decision: "approve", AnalystID: "analyst-2"}); err != nil {
					t.Fatal(err)
				}
			}
			refresher := &spyRefresher{}
			d := tt.decision
			d.PurchaseID = id

			if tt.wantStatus == models.ResolutionSaved {
				claimReviewFixture(t, tx, id, d.AnalystID)
			}
			res, err := store.UseBlocklistCache(refresher).Resolve(ctx, d)

			if err != nil || res.Status != tt.wantStatus || res.Blocked != (models.ReviewBlocks{}) {
				t.Fatalf("Resolve = %+v, %v; want %s with nothing blocked", res, err, tt.wantStatus)
			}
			if got := blocklistRows(t, tx, device, tt.txnID+"-card", tt.txnID+"-player"); len(got) != 0 {
				t.Errorf("entries = %+v, want none", got)
			}
			if len(refresher.refreshed) != 0 {
				t.Errorf("refreshed = %v, want none", refresher.refreshed)
			}
		})
	}
}

func TestResolveWithoutADeviceBlocksTheCardAndPlayer(t *testing.T) {
	tx := testTx(t)
	id := insertBlockablePurchase(t, tx, "rb-no-device", "review", nil)
	refresher := &spyRefresher{}

	claimReviewFixture(t, tx, id, "analyst-1")
	res, err := NewReviewStore(tx).UseBlocklistCache(refresher).Resolve(context.Background(), models.ReviewDecision{
		PurchaseID: id, Decision: "reject", AnalystID: "analyst-1", Blocks: models.ReviewBlocks{Device: true, Card: true, Player: true},
	})

	if err != nil || res.Status != models.ResolutionSaved || res.Blocked != (models.ReviewBlocks{Card: true, Player: true}) {
		t.Fatalf("Resolve = %+v, %v; want saved with the card and player blocked", res, err)
	}
	wantEntries := []string{"card:rb-no-device-card", "player:rb-no-device-player"}
	if got, want := blocklistRows(t, tx, "rb-no-device-card", "rb-no-device-player"), rejectionRows(id, wantEntries...); !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(refresher.refreshed, wantEntries) {
		t.Errorf("refreshed = %v, want %v", refresher.refreshed, wantEntries)
	}
}
