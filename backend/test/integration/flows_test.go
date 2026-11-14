//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestScoreSavesThePurchaseAndTheDecision(t *testing.T) {
	a := newApp(t)
	got := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-saved", 1999, "")), http.StatusOK)
	if got.TransactionID != "it-saved" || got.Outcome != "allow" {
		t.Fatalf("response %+v, want an allowed it-saved", got)
	}

	var outcome string
	var version, amount int64
	err := a.tx.QueryRow(context.Background(), `
		SELECT d.outcome, d.ruleset_version, p.amount_minor
		FROM purchases p JOIN decisions d ON d.purchase_id = p.id
		WHERE p.transaction_id = $1`, "it-saved").Scan(&outcome, &version, &amount)
	if err != nil {
		t.Fatalf("read the saved decision: %v", err)
	}
	if outcome != got.Outcome || int(version) != got.RulesetVersion || amount != 1999 {
		t.Fatalf("saved %s on version %d for %d, response %+v", outcome, version, amount, got)
	}
}

func TestDuplicateTransactionRule(t *testing.T) {
	a := newApp(t)
	body := purchase("it-duplicate", 1999, "")
	first := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, body), http.StatusOK)

	retry := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, body), http.StatusOK)
	if retry.Outcome != first.Outcome || retry.Score != first.Score || retry.RulesetVersion != first.RulesetVersion {
		t.Fatalf("retry %+v, want the first decision %+v", retry, first)
	}

	changed := purchase("it-duplicate", 2999, "")
	expect[map[string]any](t, a.do(t, http.MethodPost, "/score", serviceKey, changed), http.StatusConflict)

	var purchases int
	if err := a.tx.QueryRow(context.Background(), `SELECT count(*) FROM purchases WHERE transaction_id = $1`, "it-duplicate").Scan(&purchases); err != nil {
		t.Fatal(err)
	}
	if purchases != 1 {
		t.Fatalf("%d purchases saved for one transaction ID, want 1", purchases)
	}
}

func TestRulesetPublishingChangesScoring(t *testing.T) {
	a := newApp(t)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/rulesets", "", highAmountRuleset), http.StatusUnauthorized)

	type rulesetResponse struct {
		Version int    `json:"version"`
		Note    string `json:"note"`
	}
	published := expect[rulesetResponse](t, a.do(t, http.MethodPost, "/rulesets", adminToken, highAmountRuleset), http.StatusCreated)
	active := expect[rulesetResponse](t, a.do(t, http.MethodGet, "/rulesets/active", adminToken, ""), http.StatusOK)
	if active.Version != published.Version || active.Note != "integration test" {
		t.Fatalf("active %+v, want the published version %d", active, published.Version)
	}

	got := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-ruleset", 20000, "")), http.StatusOK)
	if got.Outcome != "review" || got.Score != 60 || got.RulesetVersion != published.Version {
		t.Fatalf("score %+v, want review with 60 points on version %d", got, published.Version)
	}

	invalid := `{"review_threshold": 80, "block_threshold": 50, "rules": {}}`
	expect[map[string]any](t, a.do(t, http.MethodPost, "/rulesets", adminToken, invalid), http.StatusBadRequest)
	again := expect[rulesetResponse](t, a.do(t, http.MethodGet, "/rulesets/active", adminToken, ""), http.StatusOK)
	if again.Version != published.Version {
		t.Fatalf("an invalid ruleset changed the active version to %d", again.Version)
	}
}

func TestReviewDecisionBlocksTheDevice(t *testing.T) {
	a := newApp(t)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/rulesets", adminToken, highAmountRuleset), http.StatusCreated)
	scored := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-review", 20000, "it-device")), http.StatusOK)
	if scored.Outcome != "review" {
		t.Fatalf("outcome %s, want review", scored.Outcome)
	}

	type queue struct {
		Items []struct {
			PurchaseID    string `json:"purchase_id"`
			TransactionID string `json:"transaction_id"`
		} `json:"items"`
	}
	expect[map[string]any](t, a.do(t, http.MethodGet, "/reviews", serviceKey, ""), http.StatusUnauthorized)
	purchaseID := ""
	for _, item := range expect[queue](t, a.do(t, http.MethodGet, "/reviews", reviewerToken, ""), http.StatusOK).Items {
		if item.TransactionID == "it-review" {
			purchaseID = item.PurchaseID
		}
	}
	if purchaseID == "" {
		t.Fatal("the purchase is not in the review queue")
	}

	decisionPath := fmt.Sprintf("/reviews/%s/decision", purchaseID)
	reject := `{"decision":"reject","block_device":true}`
	type decision struct {
		AnalystID     string `json:"analyst_id"`
		DeviceBlocked bool   `json:"device_blocked"`
	}
	saved := expect[decision](t, a.do(t, http.MethodPost, decisionPath, reviewerToken, reject), http.StatusCreated)
	if saved.AnalystID != analystID || !saved.DeviceBlocked {
		t.Fatalf("decision %+v, want %s and a blocked device", saved, analystID)
	}
	expect[map[string]any](t, a.do(t, http.MethodPost, decisionPath, reviewerToken, `{"decision":"approve"}`), http.StatusConflict)

	next := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-after-review", 100, "it-device")), http.StatusOK)
	if next.Outcome != "block" || len(next.FiredRules) != 1 || next.FiredRules[0].Name != "blocklist" {
		t.Fatalf("next purchase on the device %+v, want a blocklist block", next)
	}
}

// purchaseOf is a small purchase with the given card, player and device.
func purchaseOf(transactionID, cardToken, playerID, deviceID string) string {
	eventTime := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	return fmt.Sprintf(`{"transaction_id":%q,"event_time":%q,"amount_minor":100,"player_id":%q,"card_token":%q,"device_id":%q}`,
		transactionID, eventTime, playerID, cardToken, deviceID)
}

func TestReviewDecisionBlocksTheCardAndPlayer(t *testing.T) {
	a := newApp(t)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/rulesets", adminToken, highAmountRuleset), http.StatusCreated)
	scored := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-cp-review", 20000, "it-cp-device")), http.StatusOK)
	if scored.Outcome != "review" {
		t.Fatalf("outcome %s, want review", scored.Outcome)
	}
	var purchaseID int64
	if err := a.tx.QueryRow(context.Background(), `SELECT id FROM purchases WHERE transaction_id = 'it-cp-review'`).Scan(&purchaseID); err != nil {
		t.Fatalf("read the purchase under review: %v", err)
	}

	type decision struct {
		DeviceBlocked bool `json:"device_blocked"`
		CardBlocked   bool `json:"card_blocked"`
		PlayerBlocked bool `json:"player_blocked"`
	}
	reject := `{"decision":"reject","block_card":true,"block_player":true}`
	saved := expect[decision](t, a.do(t, http.MethodPost, fmt.Sprintf("/reviews/%d/decision", purchaseID), reviewerToken, reject), http.StatusCreated)
	if saved != (decision{CardBlocked: true, PlayerBlocked: true}) {
		t.Fatalf("decision %+v, want the card and player blocked", saved)
	}

	for name, body := range map[string]string{
		"same card":   purchaseOf("it-cp-same-card", "card-it-cp-review", "it-cp-other-player", "it-cp-other-device"),
		"same player": purchaseOf("it-cp-same-player", "it-cp-other-card", "player-it-cp-review", "it-cp-device-2"),
	} {
		next := expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, body), http.StatusOK)
		if next.Outcome != "block" || len(next.FiredRules) != 1 || next.FiredRules[0].Name != "blocklist" {
			t.Fatalf("next purchase with the %s %+v, want a blocklist block", name, next)
		}
	}
}

func TestReviewNetworkListsThePurchasesOfALinkedPlayer(t *testing.T) {
	a := newApp(t)
	expect[map[string]any](t, a.do(t, http.MethodPost, "/rulesets", adminToken, highAmountRuleset), http.StatusCreated)
	expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-net-other", 100, "it-net-device")), http.StatusOK)
	expect[scoreResponse](t, a.do(t, http.MethodPost, "/score", serviceKey, purchase("it-net-review", 20000, "it-net-device")), http.StatusOK)
	var purchaseID int64
	err := a.tx.QueryRow(context.Background(), `SELECT id FROM purchases WHERE transaction_id = 'it-net-review'`).Scan(&purchaseID)
	if err != nil {
		t.Fatalf("read the purchase under review: %v", err)
	}

	type linkedPurchases struct {
		Total     int64 `json:"total"`
		Purchases []struct {
			TransactionID  string `json:"transaction_id"`
			Outcome        string `json:"outcome"`
			AwaitingReview bool   `json:"awaiting_review"`
		} `json:"purchases"`
	}
	path := fmt.Sprintf("/reviews/%d/network/purchases?anchor=device&value=player-it-net-other", purchaseID)
	expect[map[string]any](t, a.do(t, http.MethodGet, path, serviceKey, ""), http.StatusUnauthorized)
	got := expect[linkedPurchases](t, a.do(t, http.MethodGet, path, reviewerToken, ""), http.StatusOK)
	if got.Total != 1 || len(got.Purchases) != 1 || got.Purchases[0].TransactionID != "it-net-other" ||
		got.Purchases[0].Outcome != "allow" || got.Purchases[0].AwaitingReview {
		t.Fatalf("linked purchases %+v, want the allowed it-net-other", got)
	}
	expect[map[string]any](t, a.do(t, http.MethodGet, fmt.Sprintf("/reviews/%d/network/purchases?anchor=ip&value=x", purchaseID), reviewerToken, ""), http.StatusBadRequest)
}

func TestRuntimeRoleCannotReadLabelsOrChangeDecisions(t *testing.T) {
	if !runtimeRole {
		t.Skip("TEST_RUNTIME_DATABASE_URL is not set")
	}
	ctx := context.Background()
	for _, query := range []string{
		`SELECT count(*) FROM purchase_labels`,
		`SELECT count(*) FROM pending_purchase_labels`,
		`UPDATE decisions SET outcome = 'allow' WHERE false`,
		`DELETE FROM purchases WHERE false`,
	} {
		t.Run(query, func(t *testing.T) {
			_, err := runtimePool.Exec(ctx, query)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("err = %v, want permission denied (42501)", err)
			}
		})
	}
}
