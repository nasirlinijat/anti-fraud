//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"testing"
)

type batchClaims struct {
	Results []struct {
		PurchaseID string  `json:"purchase_id"`
		Status     string  `json:"status"`
		ClaimedBy  *string `json:"claimed_by"`
	} `json:"results"`
}

type batchDecisions struct {
	AnalystID string `json:"analyst_id"`
	Results   []struct {
		PurchaseID    string `json:"purchase_id"`
		Status        string `json:"status"`
		DeviceBlocked bool   `json:"device_blocked"`
	} `json:"results"`
}

// scoreRing scores purchases on one device that the high amount rule sends
// to review and returns their purchase IDs in order.
func scoreRing(t *testing.T, a app, device string, transactionIDs ...string) []string {
	t.Helper()
	ids := make([]string, 0, len(transactionIDs))
	for _, transactionID := range transactionIDs {
		scored := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase(transactionID, 20000, device)), http.StatusOK)
		if scored.Outcome != "review" {
			t.Fatalf("%s outcome %s, want review", transactionID, scored.Outcome)
		}
		var id int64
		if err := a.tx.QueryRow(context.Background(), `SELECT id FROM purchases WHERE transaction_id = $1`, transactionID).Scan(&id); err != nil {
			t.Fatalf("read purchase %s: %v", transactionID, err)
		}
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	return ids
}

func TestLinkedReviewsAreClaimedAndRejectedTogether(t *testing.T) {
	a := newApp(t)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/rulesets", operatorKey, highAmountRuleset), http.StatusCreated)
	ring := scoreRing(t, a, "it-ring-device", "it-ring-case", "it-ring-1", "it-ring-2", "it-ring-3")
	alice, bob := a.signIn(t, "it-ring-alice"), a.signIn(t, "it-ring-bob")

	type linkedReviews struct {
		Total int64 `json:"total"`
		Items []struct {
			PurchaseID string `json:"purchase_id"`
		} `json:"items"`
	}
	linked := expect[linkedReviews](t, a.doAs(t, http.MethodGet, "/reviews/"+ring[0]+"/network/reviews", alice, ""), http.StatusOK)
	var linkedIDs []string
	for _, item := range linked.Items {
		linkedIDs = append(linkedIDs, item.PurchaseID)
	}
	if linked.Total != 3 || !reflect.DeepEqual(linkedIDs, ring[1:]) {
		t.Fatalf("linked reviews total %d, %v; want 3, %v", linked.Total, linkedIDs, ring[1:])
	}

	expect[map[string]any](t, a.doAs(t, http.MethodPost, "/reviews/"+ring[2]+"/claim", bob, ""), http.StatusCreated)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/reviews/"+ring[3]+"/decision", analystKey, `{"decision":"approve"}`), http.StatusCreated)
	ids := fmt.Sprintf(`["%s","%s","%s","%s"]`, ring[0], ring[1], ring[2], ring[3])

	claims := expect[batchClaims](t, a.doAs(t, http.MethodPost, "/reviews/claims", alice, `{"purchase_ids":`+ids+`}`), http.StatusOK)
	var claimStatuses []string
	for _, result := range claims.Results {
		claimStatuses = append(claimStatuses, result.Status)
	}
	if !reflect.DeepEqual(claimStatuses, []string{"claimed", "claimed", "held_by_other", "already_decided"}) ||
		*claims.Results[2].ClaimedBy != "it-ring-bob" || claims.Results[3].ClaimedBy != nil {
		t.Fatalf("batch claim results %+v", claims.Results)
	}

	reject := `{"decision":"reject","block_device":true,"purchase_ids":` + ids + `}`
	decided := expect[batchDecisions](t, a.doAs(t, http.MethodPost, "/reviews/decisions", alice, reject), http.StatusOK)
	var decisionStatuses []string
	for _, result := range decided.Results {
		decisionStatuses = append(decisionStatuses, result.Status)
	}
	// Each purchase is decided on its own, so the second rejection finds the
	// shared device already blocked by the first.
	if decided.AnalystID != "it-ring-alice" || !reflect.DeepEqual(decisionStatuses, []string{"saved", "saved", "not_claimed", "already_decided"}) ||
		!decided.Results[0].DeviceBlocked || decided.Results[1].DeviceBlocked {
		t.Fatalf("batch decision %+v", decided)
	}

	next := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-ring-after", 100, "it-ring-device")), http.StatusOK)
	if next.Outcome != "block" || len(next.FiredRules) != 1 || next.FiredRules[0].Name != "blocklist" {
		t.Fatalf("next purchase on the ring device %+v, want a blocklist block", next)
	}
}
