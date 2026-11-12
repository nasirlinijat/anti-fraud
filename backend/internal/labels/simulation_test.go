package labels_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
)

func TestLabeledSimulationCheckpoint(t *testing.T) {
	a, b := setup(t)
	id, run := "af111-labeled", "af111-labeled-run"
	purchase(t, a, id)
	_, e := a.Exec(t.Context(), `INSERT INTO simulation_runs(id,operator_id,request_id,settings,status) VALUES($1,'op',$1,'{"request_id":"af111-labeled-run","mode":"live","rate":5,"seed":1,"count":0,"scenarios":["normal"]}','running')`, run)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = a.Exec(context.Background(), `DELETE FROM simulation_runs WHERE id=$1`, run) })

	store := labels.NewSimulationStore(storage.NewSimulationStore(a, os.Getenv("TEST_DATABASE_URL")), b)
	c, ok, e := store.Lock(t.Context())
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	defer c.Close()
	if running, e := c.MarkPending(t.Context(), run, id); e != nil || !running {
		t.Fatal(running, e)
	}
	// The request must match the purchase saved above, which Checkpoint verifies.
	p := simulator.Purchase{Pattern: "normal", Request: simulator.Request{TransactionID: id, PlayerID: id, CardToken: id, AmountMinor: 100, EventTime: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)}}
	if e = c.PrepareLabel(t.Context(), run, p); e != nil {
		t.Fatal(e)
	}
	if e = c.Checkpoint(t.Context(), run, id, "allow"); e != nil {
		t.Fatal(e)
	}

	var saved, pending int
	a.QueryRow(t.Context(), `SELECT count(*) FROM purchase_labels WHERE transaction_id=$1 AND run_id=$2`, id, run).Scan(&saved)
	a.QueryRow(t.Context(), `SELECT count(*) FROM pending_purchase_labels WHERE transaction_id=$1`, id).Scan(&pending)
	if saved != 1 || pending != 0 {
		t.Fatalf("labels saved %d, pending %d; want 1 and 0", saved, pending)
	}
	r, e := store.Get(t.Context(), run)
	if e != nil || r.Sent != 1 || r.Allowed != 1 || r.Pending {
		t.Fatalf("%+v %v", r, e)
	}
}
