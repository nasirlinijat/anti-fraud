package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

func readNetworkPurchases(t *testing.T, store *ReviewStore, seedID int64, anchor models.BlocklistType, value string) models.LinkedPurchases {
	t.Helper()
	got, err := store.NetworkPurchases(context.Background(), seedID, anchor, value)
	if err != nil {
		t.Fatalf("NetworkPurchases(%s, %s): %v", anchor, value, err)
	}
	return got
}

func linkedPurchase(id int64, txnID string, at time.Time, outcome scoring.Outcome) models.LinkedPurchase {
	return models.LinkedPurchase{PurchaseID: id, TransactionID: txnID, EventTime: at, AmountMinor: 1000, Outcome: outcome}
}

func TestNetworkPurchasesListsTheDevicePurchasesOfALinkedPlayer(t *testing.T) {
	tx := testTx(t)
	store := NewReviewStore(tx)
	device, otherDevice := "netp-device", "netp-other-device"
	day := 24 * time.Hour
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"netp-seed", "netp-player", "netp-card", &device, networkBase, "review"})
	linked := func(txnID string, deviceID *string, at time.Time, outcome string) int64 {
		return insertNetworkPurchase(t, tx, networkPurchase{txnID, "netp-linked", "netp-card-" + txnID, deviceID, at, outcome})
	}
	linked("netp-early", &device, networkBase.Add(-31*day), "allow")
	first := linked("netp-first", &device, networkBase.Add(-30*day), "allow")
	tie1 := linked("netp-tie-1", &device, networkBase.Add(-2*time.Hour), "block")
	tie2 := linked("netp-tie-2", &device, networkBase.Add(-2*time.Hour), "allow")
	rejected := linked("netp-rejected", &device, networkBase.Add(-time.Hour), "review")
	insertNetworkResolution(t, tx, rejected, "reject")
	claimed := linked("netp-claimed", &device, networkBase.Add(time.Hour), "review")
	decided := linked("netp-decided", &device, networkBase.Add(2*time.Hour), "review")
	last := linked("netp-last", &device, networkBase.Add(30*day), "allow")
	linked("netp-late", &device, networkBase.Add(30*day+time.Second), "allow")
	linked("netp-other-device", &otherDevice, networkBase, "allow")
	linked("netp-no-device", nil, networkBase, "allow")
	insertNetworkPurchase(t, tx, networkPurchase{"netp-other-player", "netp-other", "netp-card-x", &device, networkBase, "allow"})
	createTestAccount(t, tx, "netp-alice")
	createTestAccount(t, tx, "netp-bob")
	claimNetworkPurchase(t, store, claimed, "netp-alice")
	claimNetworkPurchase(t, store, decided, "netp-bob")
	insertNetworkResolution(t, tx, decided, "approve")

	got := readNetworkPurchases(t, store, seedID, models.BlocklistDevice, "netp-linked")

	alice, approve, reject := "netp-alice", "approve", "reject"
	want := models.LinkedPurchases{Anchor: models.BlocklistDevice, Value: "netp-linked", WindowDays: 30, Total: 7, Purchases: []models.LinkedPurchase{
		linkedPurchase(last, "netp-last", networkBase.Add(30*day), scoring.OutcomeAllow),
		withReview(linkedPurchase(decided, "netp-decided", networkBase.Add(2*time.Hour), scoring.OutcomeReview), &approve, nil, false),
		withReview(linkedPurchase(claimed, "netp-claimed", networkBase.Add(time.Hour), scoring.OutcomeReview), nil, &alice, true),
		withReview(linkedPurchase(rejected, "netp-rejected", networkBase.Add(-time.Hour), scoring.OutcomeReview), &reject, nil, false),
		linkedPurchase(tie2, "netp-tie-2", networkBase.Add(-2*time.Hour), scoring.OutcomeAllow),
		linkedPurchase(tie1, "netp-tie-1", networkBase.Add(-2*time.Hour), scoring.OutcomeBlock),
		linkedPurchase(first, "netp-first", networkBase.Add(-30*day), scoring.OutcomeAllow),
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NetworkPurchases =\n%+v\nwant\n%+v", got, want)
	}
}

func withReview(p models.LinkedPurchase, decision, claimedBy *string, awaiting bool) models.LinkedPurchase {
	p.ReviewDecision, p.ClaimedBy, p.AwaitingReview = decision, claimedBy, awaiting
	return p
}

func claimNetworkPurchase(t *testing.T, store *ReviewStore, purchaseID int64, username string) {
	t.Helper()
	claim, err := store.Claim(context.Background(), purchaseID, username)
	if err != nil || claim.Status != models.ClaimSaved {
		t.Fatalf("claim %d for %s = %+v, %v", purchaseID, username, claim, err)
	}
}

func TestNetworkPurchasesListsOnlyPurchasesThatShareTheAnchor(t *testing.T) {
	tx := testTx(t)
	store := NewReviewStore(tx)
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"netp-anchor-seed", "netp-anchor-player", "netp-anchor-card", nil, networkBase, "review"})
	onCard := insertNetworkPurchase(t, tx, networkPurchase{"netp-anchor-on-card", "netp-anchor-linked", "netp-anchor-card", nil, networkBase.Add(-time.Hour), "allow"})
	insertNetworkPurchase(t, tx, networkPurchase{"netp-anchor-off-card", "netp-anchor-linked", "netp-anchor-card-2", nil, networkBase.Add(-time.Hour), "allow"})
	ofPlayer := insertNetworkPurchase(t, tx, networkPurchase{"netp-anchor-of-player", "netp-anchor-player", "netp-anchor-card-k", nil, networkBase.Add(time.Hour), "block"})
	insertNetworkPurchase(t, tx, networkPurchase{"netp-anchor-other-player", "netp-anchor-linked", "netp-anchor-card-k", nil, networkBase.Add(time.Hour), "allow"})

	for _, tc := range []struct {
		anchor models.BlocklistType
		value  string
		want   []models.LinkedPurchase
	}{
		{models.BlocklistCard, "netp-anchor-linked", []models.LinkedPurchase{
			linkedPurchase(onCard, "netp-anchor-on-card", networkBase.Add(-time.Hour), scoring.OutcomeAllow),
		}},
		{models.BlocklistPlayer, "netp-anchor-card-k", []models.LinkedPurchase{
			linkedPurchase(ofPlayer, "netp-anchor-of-player", networkBase.Add(time.Hour), scoring.OutcomeBlock),
		}},
	} {
		t.Run(string(tc.anchor), func(t *testing.T) {
			got := readNetworkPurchases(t, store, seedID, tc.anchor, tc.value)

			want := models.LinkedPurchases{Anchor: tc.anchor, Value: tc.value, WindowDays: 30, Total: 1, Purchases: tc.want}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("NetworkPurchases =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestNetworkPurchasesCapsThePage(t *testing.T) {
	tx := testTx(t)
	seedID := insertNetworkPurchase(t, tx, networkPurchase{"netp-cap-seed", "netp-cap-player", "netp-cap-card", nil, networkBase, "review"})
	for i := range 52 {
		insertNetworkPurchase(t, tx, networkPurchase{fmt.Sprintf("netp-cap-%02d", i), "netp-cap-player", "netp-cap-linked", nil, networkBase.Add(time.Duration(i) * time.Minute), "allow"})
	}

	got := readNetworkPurchases(t, NewReviewStore(tx), seedID, models.BlocklistPlayer, "netp-cap-linked")

	if got.Total != 52 || len(got.Purchases) != 50 ||
		got.Purchases[0].TransactionID != "netp-cap-51" || got.Purchases[49].TransactionID != "netp-cap-02" {
		t.Fatalf("NetworkPurchases has total %d and %d purchases: %+v", got.Total, len(got.Purchases), got.Purchases)
	}
}

func TestNetworkPurchasesOfUnlinkedValuesAreEmpty(t *testing.T) {
	tx := testTx(t)
	store := NewReviewStore(tx)
	device := "netp-empty-device"
	withDevice := insertNetworkPurchase(t, tx, networkPurchase{"netp-empty-seed", "netp-empty-player", "netp-empty-card", &device, networkBase, "review"})
	withoutDevice := insertNetworkPurchase(t, tx, networkPurchase{"netp-empty-nodevice", "netp-empty-player-2", "netp-empty-card-2", nil, networkBase, "review"})
	insertNetworkPurchase(t, tx, networkPurchase{"netp-empty-own-card", "netp-empty-player", "netp-empty-card", &device, networkBase.Add(time.Hour), "allow"})

	for _, tc := range []struct {
		name   string
		seedID int64
		anchor models.BlocklistType
		value  string
	}{
		{"unknown player", withDevice, models.BlocklistDevice, "netp-empty-unknown"},
		{"own player on the device", withDevice, models.BlocklistDevice, "netp-empty-player"},
		{"own card of the player", withDevice, models.BlocklistPlayer, "netp-empty-card"},
		{"device anchor without a device", withoutDevice, models.BlocklistDevice, "netp-empty-player"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readNetworkPurchases(t, store, tc.seedID, tc.anchor, tc.value)

			want := models.LinkedPurchases{Anchor: tc.anchor, Value: tc.value, WindowDays: 30, Purchases: []models.LinkedPurchase{}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("NetworkPurchases = %+v, want %+v", got, want)
			}
		})
	}
}

func TestNetworkPurchasesNotFoundForPurchasesNotAwaitingReview(t *testing.T) {
	tx := testTx(t)
	allowed := insertNetworkPurchase(t, tx, networkPurchase{"netp-missing-allow", "netp-missing-player", "netp-missing-card", nil, networkBase, "allow"})
	decided := insertNetworkPurchase(t, tx, networkPurchase{"netp-missing-decided", "netp-missing-player", "netp-missing-card", nil, networkBase, "review"})
	insertNetworkResolution(t, tx, decided, "approve")
	store := NewReviewStore(tx)

	for name, id := range map[string]int64{"missing": 9223372036854775807, "allowed": allowed, "decided": decided} {
		t.Run(name, func(t *testing.T) {
			_, err := store.NetworkPurchases(context.Background(), id, models.BlocklistCard, "netp-other")

			if !errors.Is(err, models.ErrReviewNotFound) {
				t.Fatalf("NetworkPurchases error = %v, want ErrReviewNotFound", err)
			}
		})
	}
}
