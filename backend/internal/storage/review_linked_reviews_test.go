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

func setRuleScore(t *testing.T, tx pgx.Tx, purchaseID int64, score int) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), `UPDATE decisions SET rule_score = $2 WHERE purchase_id = $1`, purchaseID, score); err != nil {
		t.Fatalf("set rule score of %d: %v", purchaseID, err)
	}
}

func readLinkedReviews(t *testing.T, store *ReviewStore, seedID int64) models.LinkedReviews {
	t.Helper()
	got, err := store.LinkedReviews(context.Background(), seedID)
	if err != nil {
		t.Fatalf("LinkedReviews(%d): %v", seedID, err)
	}
	return got
}

func linkedTransactionIDs(items []models.QueueItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.TransactionID)
	}
	return ids
}

// linkedReviewRing inserts the purchases of one test, each with its own rule
// score, so that the queue order can be checked.
type linkedReviewRing struct {
	tx pgx.Tx
	t  *testing.T
}

func (r linkedReviewRing) add(txnID, playerID, card string, deviceID *string, at time.Time, outcome string, score int) int64 {
	r.t.Helper()
	id := insertNetworkPurchase(r.t, r.tx, networkPurchase{txnID, playerID, card, deviceID, at, outcome})
	setRuleScore(r.t, r.tx, id, score)
	return id
}

func TestLinkedReviewsListsOtherOpenReviewsThatShareAnAnchor(t *testing.T) {
	tx := testTx(t)
	store := NewReviewStore(tx)
	device, otherDevice := "lr-device", "lr-other-device"
	day := 24 * time.Hour
	ring := linkedReviewRing{tx: tx, t: t}
	seedID := ring.add("lr-seed", "lr-player", "lr-card", &device, networkBase, "review", 99)
	ring.add("lr-via-all", "lr-player", "lr-card", &device, networkBase.Add(3*time.Hour), "review", 90)
	viaCard := ring.add("lr-via-card", "lr-other-1", "lr-card", nil, networkBase.Add(-time.Hour), "review", 80)
	ring.add("lr-via-device-early", "lr-other-2", "lr-card-2", &device, networkBase.Add(-2*time.Hour), "review", 70)
	ring.add("lr-via-device", "lr-other-3", "lr-card-3", &device, networkBase.Add(time.Hour), "review", 70)
	ring.add("lr-via-player", "lr-player", "lr-card-4", &otherDevice, networkBase.Add(2*time.Hour), "review", 60)
	ring.add("lr-window-start", "lr-other-4", "lr-card-5", &device, networkBase.Add(-30*day), "review", 10)
	ring.add("lr-window-end", "lr-other-5", "lr-card-6", &device, networkBase.Add(30*day), "review", 10)
	ring.add("lr-before-window", "lr-other-6", "lr-card-7", &device, networkBase.Add(-30*day-time.Second), "review", 95)
	ring.add("lr-after-window", "lr-other-7", "lr-card-8", &device, networkBase.Add(30*day+time.Second), "review", 95)
	ring.add("lr-allowed", "lr-other-8", "lr-card", &device, networkBase, "allow", 95)
	ring.add("lr-blocked", "lr-player", "lr-card-9", nil, networkBase, "block", 95)
	decided := ring.add("lr-decided", "lr-other-9", "lr-card-10", &device, networkBase, "review", 95)
	insertNetworkResolution(t, tx, decided, "reject")
	ring.add("lr-unrelated", "lr-other-10", "lr-card-11", &otherDevice, networkBase, "review", 95)
	createTestAccount(t, tx, "lr-alice")
	claimNetworkPurchase(t, store, viaCard, "lr-alice")

	got := readLinkedReviews(t, store, seedID)

	want := []string{"lr-via-all", "lr-via-card", "lr-via-device-early", "lr-via-device", "lr-via-player", "lr-window-start", "lr-window-end"}
	if got.WindowDays != 30 || got.Total != 7 || !reflect.DeepEqual(linkedTransactionIDs(got.Items), want) {
		t.Fatalf("LinkedReviews = window %d, total %d, %v; want 30, 7, %v", got.WindowDays, got.Total, linkedTransactionIDs(got.Items), want)
	}
	claimed := got.Items[1]
	if claimed.PurchaseID != viaCard || claimed.PlayerID != "lr-other-1" || claimed.DeviceID != nil || claimed.RuleScore != 80 ||
		claimed.ClaimedBy == nil || *claimed.ClaimedBy != "lr-alice" || claimed.ClaimedAt == nil {
		t.Fatalf("claimed item = %+v, want lr-via-card claimed by lr-alice", claimed)
	}
	if got.Items[0].ClaimedBy != nil || got.Items[0].ClaimedAt != nil {
		t.Fatalf("unclaimed item = %+v, want no claim", got.Items[0])
	}
}

func TestLinkedReviewsOfACaseWithoutADeviceLinkThroughCardAndPlayer(t *testing.T) {
	tx := testTx(t)
	device := "lr-nodev-device"
	ring := linkedReviewRing{tx: tx, t: t}
	seedID := ring.add("lr-nodev-seed", "lr-nodev-player", "lr-nodev-card", nil, networkBase, "review", 60)
	ring.add("lr-nodev-other-nodevice", "lr-nodev-other-1", "lr-nodev-card-1", nil, networkBase, "review", 60)
	ring.add("lr-nodev-on-device", "lr-nodev-other-2", "lr-nodev-card-2", &device, networkBase, "review", 60)
	ring.add("lr-nodev-via-card", "lr-nodev-other-3", "lr-nodev-card", &device, networkBase, "review", 50)
	ring.add("lr-nodev-via-player", "lr-nodev-player", "lr-nodev-card-3", nil, networkBase, "review", 40)

	got := readLinkedReviews(t, NewReviewStore(tx), seedID)

	want := []string{"lr-nodev-via-card", "lr-nodev-via-player"}
	if got.Total != 2 || !reflect.DeepEqual(linkedTransactionIDs(got.Items), want) {
		t.Fatalf("LinkedReviews = total %d, %v; want 2, %v", got.Total, linkedTransactionIDs(got.Items), want)
	}
}

func TestLinkedReviewsCapsTheList(t *testing.T) {
	tx := testTx(t)
	ring := linkedReviewRing{tx: tx, t: t}
	seedID := ring.add("lr-cap-seed", "lr-cap-player", "lr-cap-card", nil, networkBase, "review", 60)
	for i := range 52 {
		ring.add(fmt.Sprintf("lr-cap-%02d", i), fmt.Sprintf("lr-cap-other-%02d", i), "lr-cap-card", nil, networkBase.Add(time.Duration(i)*time.Minute), "review", 100-i)
	}

	got := readLinkedReviews(t, NewReviewStore(tx), seedID)

	if got.Total != 52 || len(got.Items) != 50 || got.Items[0].TransactionID != "lr-cap-00" || got.Items[49].TransactionID != "lr-cap-49" {
		t.Fatalf("LinkedReviews has total %d and %d items: %v", got.Total, len(got.Items), linkedTransactionIDs(got.Items))
	}
}

func TestLinkedReviewsWithoutOthersIsEmpty(t *testing.T) {
	tx := testTx(t)
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"lr-alone-seed", "lr-alone-player", "lr-alone-card", nil, networkBase, "review"})

	got := readLinkedReviews(t, NewReviewStore(tx), seedID)

	if got.WindowDays != 30 || got.Total != 0 || len(got.Items) != 0 {
		t.Fatalf("LinkedReviews = %+v, want an empty list", got)
	}
}

func TestLinkedReviewsNotFoundForPurchasesNotAwaitingReview(t *testing.T) {
	tx := testTx(t)
	allowed := insertNetworkPurchase(t, tx, networkPurchase{"lr-missing-allow", "lr-missing-player", "lr-missing-card", nil, networkBase, "allow"})
	decided := insertNetworkPurchase(t, tx, networkPurchase{"lr-missing-decided", "lr-missing-player", "lr-missing-card", nil, networkBase, "review"})
	insertNetworkResolution(t, tx, decided, "approve")
	store := NewReviewStore(tx)

	for name, id := range map[string]int64{"missing": 9223372036854775807, "allowed": allowed, "decided": decided} {
		t.Run(name, func(t *testing.T) {
			_, err := store.LinkedReviews(context.Background(), id)

			if !errors.Is(err, models.ErrReviewNotFound) {
				t.Fatalf("LinkedReviews error = %v, want ErrReviewNotFound", err)
			}
		})
	}
}
