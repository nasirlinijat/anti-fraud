//go:build integration

package concurrency

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

type decision struct {
	TransactionID  string `json:"transaction_id"`
	Outcome        string `json:"outcome"`
	Score          int    `json:"score"`
	RulesetVersion int    `json:"ruleset_version"`
}

func TestConcurrentScoresOfOneTransactionSaveOneDecision(t *testing.T) {
	const n = 20

	t.Run("same purchase", func(t *testing.T) {
		transactionID := unique("race-same")
		responses := sendAll(t, n, func(int) (string, string, string, string) {
			return http.MethodPost, "/score", serviceKey, purchase(transactionID, 1999)
		})
		var first *decision
		for i, r := range responses {
			if r.status != http.StatusOK && r.status != http.StatusConflict {
				t.Fatalf("request %d: status %d, body %s", i, r.status, r.body)
			}
			if r.status != http.StatusOK {
				continue
			}
			d := decode[decision](t, r)
			if first == nil {
				first = &d
			} else if d != *first {
				t.Fatalf("request %d got %+v, another got %+v; want the one saved decision", i, d, *first)
			}
		}
		if first == nil {
			t.Fatal("no request got the saved decision")
		}
		if got := count(t, `SELECT count(*) FROM purchases WHERE transaction_id = $1`, transactionID); got != 1 {
			t.Fatalf("%d purchases saved, want 1", got)
		}
		if got := count(t, `SELECT count(*) FROM decisions d JOIN purchases p ON p.id = d.purchase_id WHERE p.transaction_id = $1`, transactionID); got != 1 {
			t.Fatalf("%d decisions saved, want 1", got)
		}
	})

	t.Run("different purchases with one transaction ID", func(t *testing.T) {
		transactionID := unique("race-different")
		responses := sendAll(t, n, func(i int) (string, string, string, string) {
			return http.MethodPost, "/score", serviceKey, purchase(transactionID, int64(1000+i))
		})
		saved := 0
		for i, r := range responses {
			switch r.status {
			case http.StatusOK:
				saved++
			case http.StatusConflict:
			default:
				t.Fatalf("request %d: status %d, body %s", i, r.status, r.body)
			}
		}
		if saved != 1 {
			t.Fatalf("%d requests saved a decision, want exactly 1 and conflicts for the rest", saved)
		}
		if got := count(t, `SELECT count(*) FROM purchases WHERE transaction_id = $1`, transactionID); got != 1 {
			t.Fatalf("%d purchases saved, want 1", got)
		}
	})
}

func TestConcurrentRulesetPublishesLeaveOneActiveVersion(t *testing.T) {
	const n = 10
	before := count(t, `SELECT max(version) FROM rulesets`)

	responses := sendAll(t, n, func(int) (string, string, string, string) {
		return http.MethodPost, "/rulesets", adminToken, highAmountRule
	})
	versions := make([]int, 0, n)
	for i, r := range responses {
		if r.status != http.StatusCreated {
			t.Fatalf("request %d: status %d, body %s", i, r.status, r.body)
		}
		versions = append(versions, decode[struct {
			Version int `json:"version"`
		}](t, r).Version)
	}
	slices.Sort(versions)
	for i, v := range versions {
		if v != before+1+i {
			t.Fatalf("versions %v, want %d to %d with no gap or repeat", versions, before+1, before+n)
		}
	}
	if got := count(t, `SELECT count(*) FROM rulesets WHERE is_active`); got != 1 {
		t.Fatalf("%d active rulesets, want 1", got)
	}
	if got := count(t, `SELECT version FROM rulesets WHERE is_active`); got != before+n {
		t.Fatalf("active version %d, want the last one %d", got, before+n)
	}
}

func TestTwoAnalystsDecidingOneReviewSaveOneDecision(t *testing.T) {
	const purchases = 10
	if r, err := send(http.MethodPost, "/rulesets", adminToken, highAmountRule); err != nil || r.status != http.StatusCreated {
		t.Fatalf("publish: %v %+v", err, r)
	}

	for p := range purchases {
		transactionID := unique(fmt.Sprintf("race-review-%d", p))
		r, err := send(http.MethodPost, "/score", serviceKey, purchase(transactionID, 20000))
		if err != nil || r.status != http.StatusOK || decode[decision](t, r).Outcome != "review" {
			t.Fatalf("score %s: %v %+v", transactionID, err, r)
		}
		purchaseID := count(t, `SELECT id FROM purchases WHERE transaction_id = $1`, transactionID)
		claimPath := fmt.Sprintf("/reviews/%d/claim", purchaseID)
		if r, err := send(http.MethodPost, claimPath, firstReviewer, ""); err != nil || r.status != 201 {
			t.Fatalf("claim: %+v %v", r, err)
		}
		path := fmt.Sprintf("/reviews/%d/decision", purchaseID)

		responses := sendAll(t, 2, func(i int) (string, string, string, string) {
			if i == 0 {
				return http.MethodPost, path, firstReviewer, `{"decision":"approve"}`
			}
			return http.MethodPost, path, secondReviewer, `{"decision":"reject"}`
		})
		statuses := []int{responses[0].status, responses[1].status}
		slices.Sort(statuses)
		if statuses[0] != http.StatusCreated || statuses[1] != http.StatusConflict {
			t.Fatalf("%s: statuses %v, bodies %q and %q; want one 201 and one 409", transactionID, statuses, responses[0].body, responses[1].body)
		}
		if got := count(t, `SELECT count(*) FROM review_resolutions WHERE purchase_id = $1`, purchaseID); got != 1 {
			t.Fatalf("%s: %d decisions saved, want 1", transactionID, got)
		}
	}
}

type batchDecisionResponse struct {
	Results []struct {
		PurchaseID string `json:"purchase_id"`
		Status     string `json:"status"`
	} `json:"results"`
}

func TestTwoAnalystsDecidingOverlappingBatchesSaveOneDecisionEach(t *testing.T) {
	const purchases = 10
	if r, err := send(http.MethodPost, "/rulesets", adminToken, highAmountRule); err != nil || r.status != http.StatusCreated {
		t.Fatalf("publish: %v %+v", err, r)
	}
	ids := make([]string, purchases)
	for p := range purchases {
		transactionID := unique(fmt.Sprintf("race-batch-%d", p))
		r, err := send(http.MethodPost, "/score", serviceKey, purchase(transactionID, 20000))
		if err != nil || r.status != http.StatusOK || decode[decision](t, r).Outcome != "review" {
			t.Fatalf("score %s: %v %+v", transactionID, err, r)
		}
		ids[p] = fmt.Sprint(count(t, `SELECT id FROM purchases WHERE transaction_id = $1`, transactionID))
	}
	for i, id := range ids {
		owner := firstReviewer
		if i >= 8 {
			owner = secondReviewer
		}
		if r, err := send(http.MethodPost, "/reviews/"+id+"/claim", owner, ""); err != nil || r.status != 201 {
			t.Fatalf("claim: %+v %v", r, err)
		}
	}
	// The first analyst decides purchases 0 to 7 and the second 2 to 9, so
	// purchases 2 to 7 are decided by both at the same moment.
	batches := [][]string{ids[:8], ids[2:]}

	responses := sendAll(t, 2, func(i int) (string, string, string, string) {
		body := fmt.Sprintf(`{"decision":"approve","purchase_ids":["%s"]}`, strings.Join(batches[i], `","`))
		return http.MethodPost, "/reviews/decisions", []string{firstReviewer, secondReviewer}[i], body
	})

	statuses := map[string][]string{}
	for i, r := range responses {
		if r.status != http.StatusOK {
			t.Fatalf("analyst %d: status %d, body %s", i+1, r.status, r.body)
		}
		for _, result := range decode[batchDecisionResponse](t, r).Results {
			statuses[result.PurchaseID] = append(statuses[result.PurchaseID], result.Status)
		}
	}
	for p, id := range ids {
		got := statuses[id]
		slices.Sort(got)
		want := []string{"already_decided", "saved"}
		if p < 2 || p > 7 {
			want = []string{"saved"}
		}
		if p >= 2 && p <= 7 && slices.Equal(got, []string{"not_claimed", "saved"}) {
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("purchase %d: statuses %v, want %v", p, got, want)
		}
		if saved := count(t, `SELECT count(*) FROM review_resolutions WHERE purchase_id = $1::bigint`, id); saved != 1 {
			t.Errorf("purchase %d: %d decisions saved, want 1", p, saved)
		}
	}
}
