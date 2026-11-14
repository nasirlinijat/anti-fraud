//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/decisionstatus"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
)

type statusReply struct {
	TransactionID string     `json:"transaction_id"`
	Outcome       string     `json:"outcome"`
	ReviewStatus  string     `json:"review_status"`
	ResolvedAt    *time.Time `json:"resolved_at"`
}

func TestDecisionStatusFollowsReviewWithoutChangingScoreRetries(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			a := newApp(t)
			expect[map[string]any](t, a.do(t, "POST", "/rulesets", adminToken, highAmountRuleset), 201)
			id := "it-status-" + decision
			body := purchase(id, 20000, "")
			first := a.do(t, "POST", "/score", serviceKey, body)
			expect[scoreResponse](t, first, 200)
			path := "/decision-status?transaction_id=" + id
			pending := expect[statusReply](t, a.do(t, "GET", path, serviceKey, ""), 200)
			if pending.ReviewStatus != "pending" || pending.ResolvedAt != nil {
				t.Fatalf("%+v", pending)
			}
			var purchaseID int64
			if err := a.tx.QueryRow(t.Context(), "SELECT id FROM purchases WHERE transaction_id=$1", id).Scan(&purchaseID); err != nil {
				t.Fatal(err)
			}
			expect[map[string]any](t, a.do(t, "POST", fmt.Sprintf("/reviews/%d/decision", purchaseID), reviewerToken, fmt.Sprintf(`{"decision":%q}`, decision)), 201)
			got := expect[statusReply](t, a.do(t, "GET", path, serviceKey, ""), 200)
			want := "approved"
			if decision == "reject" {
				want = "rejected"
			}
			if got.Outcome != "review" || got.ReviewStatus != want || got.ResolvedAt == nil {
				t.Fatalf("%+v", got)
			}
			retry := a.do(t, "POST", "/score", serviceKey, body)
			if retry.Code != 200 || retry.Body.String() != first.Body.String() {
				t.Fatal("score retry changed")
			}
			expect[map[string]any](t, a.doAs(t, "GET", path, a.signIn(t, "status-reader"), ""), 401)
		})
	}
}
func TestDecisionStatusOnlySeesCommittedResolutions(t *testing.T) {
	ctx := context.Background()
	rules, err := storage.NewRulesetStore(ownerPool).Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			id := fmt.Sprintf("it-status-visible-%d", time.Now().UnixNano())
			p := models.Purchase{TransactionID: id, EventTime: time.Now().UTC(), AmountMinor: 100, PlayerID: id, CardToken: id}
			purchaseID, err := storage.NewPurchaseStore(ownerPool).Save(ctx, p, models.Decision{RulesetVersion: rules.Version, Result: scoring.Result{Outcome: scoring.OutcomeReview}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ownerPool.Exec(ctx, "DELETE FROM review_resolutions WHERE purchase_id=$1", purchaseID)
				ownerPool.Exec(ctx, "DELETE FROM decisions WHERE purchase_id=$1", purchaseID)
				ownerPool.Exec(ctx, "DELETE FROM purchases WHERE id=$1", purchaseID)
			})
			assertStatusReadOnly(t, id)
			mux := http.NewServeMux()
			transport.RegisterDecisionStatusRoutes(mux, transport.NewDecisionStatusHandler(decisionstatus.NewService(storage.NewDecisionStatusStore(runtimePool))), serviceKey)
			lookup := func() statusReply {
				req := httptest.NewRequest("GET", "/decision-status?transaction_id="+id, nil)
				req.Header.Set("X-API-Key", serviceKey)
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				return expect[statusReply](t, rec, 200)
			}
			tx, err := ownerPool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, "INSERT INTO review_resolutions(purchase_id,decision,analyst_id) VALUES($1,'approve','test')", purchaseID); err != nil {
				t.Fatal(err)
			}
			if got := lookup(); got.ReviewStatus != "pending" {
				t.Fatalf("uncommitted result: %+v", got)
			}
			want := "pending"
			if commit {
				err = tx.Commit(ctx)
				want = "approved"
			} else {
				err = tx.Rollback(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := lookup(); got.ReviewStatus != want {
				t.Fatalf("%+v want %s", got, want)
			}
		})
	}
}

func TestDecisionStatusReadsDirectOutcomesAndMissingPurchase(t *testing.T) {
	a := newApp(t)
	expect[map[string]any](t, a.do(t, "POST", "/rulesets", adminToken, highAmountRuleset), 201)
	expect[scoreResponse](t, a.do(t, "POST", "/score", serviceKey, purchase("status-direct-allow", 100, "")), 200)
	expect[map[string]any](t, a.do(t, "POST", "/rulesets", adminToken, strings.Replace(highAmountRuleset, "60", "90", 1)), 201)
	expect[scoreResponse](t, a.do(t, "POST", "/score", serviceKey, purchase("status-direct-block", 20000, "")), 200)
	for _, outcome := range []string{"allow", "block"} {
		id := "status-direct-" + outcome
		got := expect[statusReply](t, a.do(t, "GET", "/decision-status?transaction_id="+id, serviceKey, ""), 200)
		if got.Outcome != outcome || got.ReviewStatus != "not_required" || got.ResolvedAt != nil {
			t.Fatalf("%+v", got)
		}
	}
	expect[map[string]any](t, a.do(t, "GET", "/decision-status?transaction_id=unknown", serviceKey, ""), 404)
}

func assertStatusReadOnly(t *testing.T, id string) {
	t.Helper()
	tx, err := runtimePool.BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	got, err := storage.NewDecisionStatusStore(tx).Find(t.Context(), id)
	if err != nil || got.TransactionID != id {
		t.Fatalf("read-only lookup: %+v %v", got, err)
	}
}
