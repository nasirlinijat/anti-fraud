package labels_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

type scoreFunc func(context.Context, simulator.Request) (simulator.Result, error)

func (f scoreFunc) Score(c context.Context, r simulator.Request) (simulator.Result, error) {
	return f(c, r)
}
func TestRecoveryVerifiesPurchaseIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		matching bool
		want     int
	}{{"observed409", 409, false, 0}, {"lost409", 0, false, 0}, {"lostSuccess", 0, true, 1}, {"observed401", 401, true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := setup(t)
			id := "identity-" + tc.name
			purchase(t, a, id)
			var at time.Time
			if err := a.QueryRow(t.Context(), `SELECT event_time FROM purchases WHERE transaction_id=$1`, id).Scan(&at); err != nil {
				t.Fatal(err)
			}
			req := simulator.Request{TransactionID: id, PlayerID: id, CardToken: id, AmountMinor: 100, EventTime: at}
			if !tc.matching {
				req.AmountMinor = 200
			}
			p := simulator.Purchase{Pattern: simulator.PatternCardTesting, Request: req}
			var client simulator.Scorer = scoreFunc(func(context.Context, simulator.Request) (simulator.Result, error) {
				return simulator.Result{}, io.ErrUnexpectedEOF
			})
			if tc.status != 0 {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"error":"rejected"}`))
				}))
				defer server.Close()
				client = &simulator.Client{BaseURL: server.URL, APIKey: "test", HTTP: server.Client()}
			}
			s := labels.Scorer{Pool: b, Client: client, RunID: "run-" + tc.name}
			if _, err := s.ScorePurchase(t.Context(), p); err == nil {
				t.Fatal("expected uncertain or rejected request")
			}
			for i := range 2 {
				n, err := labels.Recover(t.Context(), b, s.RunID)
				if err != nil {
					t.Fatal(err)
				}
				want := tc.want
				if i == 1 {
					want = 0
				}
				if n != want {
					t.Fatalf("recovered %d want %d", n, want)
				}
			}
			var n int
			if err := b.QueryRow(t.Context(), `SELECT count(*) FROM purchase_labels WHERE transaction_id=$1`, id).Scan(&n); err != nil || n != tc.want {
				t.Fatal(n, err)
			}
			if tc.want == 0 {
				var reason *string
				if err := b.QueryRow(t.Context(), `SELECT rejection_reason FROM pending_purchase_labels WHERE transaction_id=$1`, id).Scan(&reason); err != nil || reason == nil {
					t.Fatal("untrusted intent not quarantined", err)
				}
			}
		})
	}
}
func TestFinalizeRefusesDifferentPurchase(t *testing.T) {
	a, b := setup(t)
	id := "identity-finalize"
	purchase(t, a, id)
	var at time.Time
	a.QueryRow(t.Context(), `SELECT event_time FROM purchases WHERE transaction_id=$1`, id).Scan(&at)
	l := labels.FromPurchase("run", simulator.Purchase{Pattern: simulator.PatternNormal, Request: simulator.Request{TransactionID: id, PlayerID: id, CardToken: id, AmountMinor: 999, EventTime: at}})
	if err := labels.Finalize(t.Context(), b, l, nil); !errors.Is(err, labels.ErrPurchaseMismatch) {
		t.Fatal(err)
	}
}
func TestSnapshotMatchesScoringNormalization(t *testing.T) {
	a, b := setup(t)
	id := "identity-normalized"
	purchase(t, a, id)
	var at time.Time
	a.QueryRow(t.Context(), `SELECT event_time FROM purchases WHERE transaction_id=$1`, id).Scan(&at)
	empty := "  "
	p := simulator.Purchase{Pattern: simulator.PatternNormal, Request: simulator.Request{TransactionID: " " + id + " ", PlayerID: " " + id + " ", CardToken: id + " ", AmountMinor: 100, EventTime: at.In(time.FixedZone("offset", 4*3600)).Add(321 * time.Nanosecond), CardIssuer: &empty}}
	l := labels.FromPurchase("run", p)
	if err := labels.Prepare(t.Context(), b, l); err != nil {
		t.Fatal(err)
	}
	if n, err := labels.Recover(t.Context(), b, "run"); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestRejectedRecoveryKeepsPriorTruthAndDoesNotCountIt(t *testing.T) {
	a, b := setup(t)
	id := "identity-existing-label"
	purchase(t, a, id)
	original := labels.Label{TransactionID: id, RunID: "original-run", Source: "ieee-cis"}
	if err := labels.Finalize(t.Context(), b, original, nil); err != nil {
		t.Fatal(err)
	}
	bad := labels.FromPurchase("rejected-run", simulator.Purchase{Pattern: simulator.PatternCardTesting, Request: simulator.Request{TransactionID: id, PlayerID: id, CardToken: id, AmountMinor: 999, EventTime: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}})
	if err := labels.Prepare(t.Context(), b, bad); err != nil {
		t.Fatal(err)
	}
	if n, err := labels.Recover(t.Context(), b, "rejected-run"); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	var run string
	var fraud bool
	if err := b.QueryRow(t.Context(), `SELECT run_id,is_fraud FROM purchase_labels WHERE transaction_id=$1`, id).Scan(&run, &fraud); err != nil || run != "original-run" || fraud {
		t.Fatal(run, fraud, err)
	}
}
