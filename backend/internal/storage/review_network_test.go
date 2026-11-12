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
)

func TestNetworkStatusPrefersTheWorstFlag(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags linkedFlags
		want  models.NetworkStatus
	}{
		{"nothing known", linkedFlags{}, models.NetworkClear},
		{"in review", linkedFlags{inReview: true}, models.NetworkInReview},
		{"blocked over in review", linkedFlags{blocked: true, inReview: true}, models.NetworkBlocked},
		{"rejected over blocked", linkedFlags{rejected: true, blocked: true, inReview: true}, models.NetworkRejected},
		{"blocklisted over all", linkedFlags{blocklisted: true, rejected: true, blocked: true, inReview: true}, models.NetworkBlocklisted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := networkStatus(tc.flags); got != tc.want {
				t.Fatalf("networkStatus(%+v) = %s, want %s", tc.flags, got, tc.want)
			}
		})
	}
}

var networkBase = time.Date(2000, 3, 1, 12, 0, 0, 0, time.UTC)

type networkPurchase struct {
	txnID    string
	playerID string
	card     string
	deviceID *string
	at       time.Time
	outcome  string
}

func insertNetworkPurchase(t *testing.T, tx pgx.Tx, p networkPurchase) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO purchases (transaction_id, event_time, amount_minor, player_id, card_token, device_id)
		VALUES ($1, $2, 1000, $3, $4, $5)
		RETURNING id`, p.txnID, p.at, p.playerID, p.card, p.deviceID).Scan(&id)
	if err != nil {
		t.Fatalf("insert purchase %s: %v", p.txnID, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO decisions (purchase_id, ruleset_version, outcome, rule_score, fired_rules, model_inputs, decided_at)
		VALUES ($1, 1, $2, 60, '[]', '{}', $3)`, id, p.outcome, p.at)
	if err != nil {
		t.Fatalf("insert decision %s: %v", p.txnID, err)
	}
	return id
}

func insertNetworkResolution(t *testing.T, tx pgx.Tx, purchaseID int64, decision string) {
	t.Helper()
	_, err := tx.Exec(context.Background(), `
		INSERT INTO review_resolutions (purchase_id, decision, analyst_id) VALUES ($1, $2, 'network-analyst')`, purchaseID, decision)
	if err != nil {
		t.Fatalf("insert resolution for %d: %v", purchaseID, err)
	}
}

func insertNetworkBlocklistEntry(t *testing.T, tx pgx.Tx, entryType models.BlocklistType, value string) {
	t.Helper()
	_, err := tx.Exec(context.Background(), `
		INSERT INTO blocklist_entries (entry_type, value, reason) VALUES ($1, $2, 'network test')`, string(entryType), value)
	if err != nil {
		t.Fatalf("insert blocklist entry %s %s: %v", entryType, value, err)
	}
}

func TestNetworkLinksPlayersAndCardsWithinTheWindow(t *testing.T) {
	tx := testTx(t)
	device := "net-window-device"
	day := 24 * time.Hour
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"net-window-seed", "net-window-player", "net-window-card", &device, networkBase, "review"})
	for _, p := range []networkPurchase{
		{"net-window-dev-a1", "net-window-player-a", "net-window-card-a", &device, networkBase.Add(-29 * day), "allow"},
		{"net-window-dev-a2", "net-window-player-a", "net-window-card-a", &device, networkBase.Add(time.Hour), "allow"},
		{"net-window-dev-late", "net-window-player-b", "net-window-card-b", &device, networkBase.Add(31 * day), "allow"},
		{"net-window-dev-own", "net-window-player", "net-window-card-own", &device, networkBase.Add(-day), "allow"},
		{"net-window-card-e", "net-window-player-e", "net-window-card", nil, networkBase.Add(-day), "allow"},
		{"net-window-card-early", "net-window-player-f", "net-window-card", nil, networkBase.Add(-31 * day), "allow"},
		{"net-window-own-card", "net-window-player", "net-window-card", nil, networkBase.Add(2 * day), "allow"},
		{"net-window-player-k", "net-window-player", "net-window-card-k", nil, networkBase.Add(30 * day), "allow"},
		{"net-window-player-l", "net-window-player", "net-window-card-l", nil, networkBase.Add(-40 * day), "allow"},
	} {
		insertNetworkPurchase(t, tx, p)
	}

	got, err := NewReviewStore(tx).Network(context.Background(), seedID)

	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	want := models.CaseNetwork{WindowDays: 30, Anchors: []models.NetworkAnchor{
		{Type: models.BlocklistDevice, Value: device, LinkedTotal: 1, Linked: []models.LinkedEntity{
			{Type: models.BlocklistPlayer, Value: "net-window-player-a", Purchases: 2, Status: models.NetworkClear},
		}},
		{Type: models.BlocklistCard, Value: "net-window-card", LinkedTotal: 1, Linked: []models.LinkedEntity{
			{Type: models.BlocklistPlayer, Value: "net-window-player-e", Purchases: 1, Status: models.NetworkClear},
		}},
		{Type: models.BlocklistPlayer, Value: "net-window-player", LinkedTotal: 2, Linked: []models.LinkedEntity{
			{Type: models.BlocklistCard, Value: "net-window-card-k", Purchases: 1, Status: models.NetworkClear},
			{Type: models.BlocklistCard, Value: "net-window-card-own", Purchases: 1, Status: models.NetworkClear},
		}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Network =\n%+v\nwant\n%+v", got, want)
	}
}

func TestNetworkOrdersLinkedEntitiesWorstStatusFirst(t *testing.T) {
	tx := testTx(t)
	device := "net-status-device"
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"net-status-seed", "net-status-player", "net-status-card", &device, networkBase, "review"})
	onDevice := func(txnID, playerID, outcome string) int64 {
		return insertNetworkPurchase(t, tx, networkPurchase{txnID, playerID, "net-status-card-" + txnID, &device, networkBase.Add(-time.Hour), outcome})
	}
	onDevice("clear-1", "net-status-clear-one", "allow")
	onDevice("clear-2a", "net-status-clear-two", "allow")
	insertNetworkResolution(t, tx, onDevice("clear-2b", "net-status-clear-two", "review"), "approve")
	onDevice("review", "net-status-in-review", "review")
	onDevice("block", "net-status-blocked", "block")
	onDevice("rejected-block", "net-status-rejected", "block")
	insertNetworkResolution(t, tx, onDevice("rejected-review", "net-status-rejected", "review"), "reject")
	onDevice("blocklisted", "net-status-blocklisted", "allow")
	insertNetworkBlocklistEntry(t, tx, models.BlocklistPlayer, "net-status-blocklisted")
	insertNetworkBlocklistEntry(t, tx, models.BlocklistDevice, device)

	got, err := NewReviewStore(tx).Network(context.Background(), seedID)

	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	if len(got.Anchors) != 3 || got.Anchors[0].Type != models.BlocklistDevice {
		t.Fatalf("anchors = %+v", got.Anchors)
	}
	deviceAnchor := got.Anchors[0]
	want := []models.LinkedEntity{
		{Type: models.BlocklistPlayer, Value: "net-status-blocklisted", Purchases: 1, Status: models.NetworkBlocklisted},
		{Type: models.BlocklistPlayer, Value: "net-status-rejected", Purchases: 2, Status: models.NetworkRejected},
		{Type: models.BlocklistPlayer, Value: "net-status-blocked", Purchases: 1, Status: models.NetworkBlocked},
		{Type: models.BlocklistPlayer, Value: "net-status-in-review", Purchases: 1, Status: models.NetworkInReview},
		{Type: models.BlocklistPlayer, Value: "net-status-clear-two", Purchases: 2, Status: models.NetworkClear},
		{Type: models.BlocklistPlayer, Value: "net-status-clear-one", Purchases: 1, Status: models.NetworkClear},
	}
	if !deviceAnchor.Blocklisted || deviceAnchor.LinkedTotal != 6 || !reflect.DeepEqual(deviceAnchor.Linked, want) {
		t.Fatalf("device anchor =\n%+v\nwant blocklisted, 6 linked:\n%+v", deviceAnchor, want)
	}
	if got.Anchors[1].Blocklisted || got.Anchors[2].Blocklisted {
		t.Fatalf("card and player anchors are not blocklisted: %+v", got.Anchors[1:])
	}
}

func TestNetworkOrdersByPurchasesWithinAStatus(t *testing.T) {
	tx := testTx(t)
	device := "net-rank-device"
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"net-rank-seed", "net-rank-player", "net-rank-card", &device, networkBase, "review"})
	onDevice := func(txnID, playerID, outcome string) {
		insertNetworkPurchase(t, tx, networkPurchase{txnID, playerID, "net-rank-card-" + txnID, &device, networkBase.Add(-time.Hour), outcome})
	}
	onDevice("pending-block", "net-rank-blocked-pending", "block")
	onDevice("pending-review", "net-rank-blocked-pending", "review")
	for _, txnID := range []string{"busy-1", "busy-2", "busy-3"} {
		onDevice(txnID, "net-rank-blocked-busy", "block")
	}

	got, err := NewReviewStore(tx).Network(context.Background(), seedID)

	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	want := []models.LinkedEntity{
		{Type: models.BlocklistPlayer, Value: "net-rank-blocked-busy", Purchases: 3, Status: models.NetworkBlocked},
		{Type: models.BlocklistPlayer, Value: "net-rank-blocked-pending", Purchases: 2, Status: models.NetworkBlocked},
	}
	if !reflect.DeepEqual(got.Anchors[0].Linked, want) {
		t.Fatalf("device links =\n%+v\nwant\n%+v", got.Anchors[0].Linked, want)
	}
}

func TestNetworkCapsLinkedEntities(t *testing.T) {
	tx := testTx(t)
	device := "net-cap-device"
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"net-cap-seed", "net-cap-player", "net-cap-card", &device, networkBase, "review"})
	for i := range 32 {
		id := fmt.Sprintf("%02d", i)
		insertNetworkPurchase(t, tx, networkPurchase{"net-cap-" + id, "net-cap-player-" + id, "net-cap-card-" + id, &device, networkBase.Add(time.Minute), "allow"})
	}

	got, err := NewReviewStore(tx).Network(context.Background(), seedID)

	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	linked := got.Anchors[0].Linked
	if got.Anchors[0].LinkedTotal != 32 || len(linked) != 30 ||
		linked[0].Value != "net-cap-player-00" || linked[29].Value != "net-cap-player-29" {
		t.Fatalf("device anchor = %+v", got.Anchors[0])
	}
}

func TestNetworkWithoutDeviceHasNoDeviceAnchor(t *testing.T) {
	tx := testTx(t)
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"net-nodevice-seed", "net-nodevice-player", "net-nodevice-card", nil, networkBase, "review"})
	insertNetworkBlocklistEntry(t, tx, models.BlocklistCard, "net-nodevice-card")
	insertNetworkBlocklistEntry(t, tx, models.BlocklistPlayer, "net-nodevice-player")

	got, err := NewReviewStore(tx).Network(context.Background(), seedID)

	if err != nil {
		t.Fatalf("Network: %v", err)
	}
	want := models.CaseNetwork{WindowDays: 30, Anchors: []models.NetworkAnchor{
		{Type: models.BlocklistCard, Value: "net-nodevice-card", Blocklisted: true, Linked: []models.LinkedEntity{}},
		{Type: models.BlocklistPlayer, Value: "net-nodevice-player", Blocklisted: true, Linked: []models.LinkedEntity{}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Network =\n%+v\nwant\n%+v", got, want)
	}
}

func TestNetworkNotFoundForPurchasesNotAwaitingReview(t *testing.T) {
	tx := testTx(t)
	allowed := insertNetworkPurchase(t, tx, networkPurchase{"net-missing-allow", "net-missing-player", "net-missing-card", nil, networkBase, "allow"})
	decided := insertNetworkPurchase(t, tx, networkPurchase{"net-missing-decided", "net-missing-player", "net-missing-card", nil, networkBase, "review"})
	insertNetworkResolution(t, tx, decided, "approve")
	store := NewReviewStore(tx)

	for name, id := range map[string]int64{"missing": 9223372036854775807, "allowed": allowed, "decided": decided} {
		t.Run(name, func(t *testing.T) {
			_, err := store.Network(context.Background(), id)

			if !errors.Is(err, models.ErrReviewNotFound) {
				t.Fatalf("Network error = %v, want ErrReviewNotFound", err)
			}
		})
	}
}
