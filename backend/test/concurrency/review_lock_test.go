//go:build integration

package concurrency

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

func TestReviewChangesWaitForCompetingTransactions(t *testing.T) {
	cases := []struct {
		name, first, method, suffix, actor string
		want                               int
	}{
		{"decision before release", "decide", "DELETE", "claim", adminToken, 409},
		{"release before decision", "release", "POST", "decision", firstReviewer, 409},
		{"claim before another claim", "claim", "POST", "claim", secondReviewer, 409},
		{"release before another claim", "release", "POST", "claim", secondReviewer, 201},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := seedClaimedReview(t)
			ctx := context.Background()
			tx, err := ownerPool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			applyReviewChange(t, tx, tc.first, id)
			finished := make(chan response, 1)
			go func() {
				result, err := send(tc.method, fmt.Sprintf("/reviews/%d/%s", id, tc.suffix), tc.actor, `{"decision":"approve"}`)
				if err != nil {
					result.body = err.Error()
				}
				finished <- result
			}()
			waitForReviewLock(t, id)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-finished:
				if result.status != tc.want {
					t.Fatalf("response: %+v, want %d", result, tc.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request did not finish after commit")
			}
			if tc.first == "decide" && count(t, "SELECT count(*) FROM review_claims WHERE purchase_id=$1", id) != 1 {
				t.Fatal("resolved purchase lost its claim")
			}
		})
	}
}

func applyReviewChange(t *testing.T, tx pgx.Tx, first string, id int64) {
	t.Helper()
	ctx := context.Background()
	store := storage.NewReviewStore(tx)
	switch first {
	case "decide":
		result, err := store.Resolve(ctx, models.ReviewDecision{PurchaseID: id, AnalystID: firstReviewer, Decision: "approve"})
		if err != nil || result.Status != models.ResolutionSaved {
			t.Fatalf("resolve: %+v %v", result, err)
		}
	case "release":
		if _, err := store.ReleaseClaim(ctx, id); err != nil {
			t.Fatal(err)
		}
	case "claim":
		if _, err := tx.Exec(ctx, "DELETE FROM review_claims WHERE purchase_id=$1", id); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Claim(ctx, id, firstReviewer); err != nil {
			t.Fatal(err)
		}
	}
}

func seedClaimedReview(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := ownerPool.QueryRow(ctx, `INSERT INTO purchases(transaction_id,event_time,amount_minor,player_id,card_token) VALUES($1,now(),1000,'lock-player','lock-card') RETURNING id`, unique("lock-review")).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ownerPool.Exec(ctx, `INSERT INTO decisions(purchase_id,ruleset_version,outcome,rule_score,fired_rules,model_inputs) VALUES($1,1,'review',60,'[]','{}')`, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ownerPool.Exec(ctx, `INSERT INTO review_claims(purchase_id,account_id) SELECT $1,id FROM accounts WHERE username=$2`, id, firstReviewer)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitForReviewLock(t *testing.T, id int64) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if count(t, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid=$1::oid`, id) > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("competing request did not wait for the review lock")
		case <-tick.C:
		}
	}
}
