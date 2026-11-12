package simulation

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

func TestEndAtMovesAccountEventsWithTheirPurchases(t *testing.T) {
	start := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	purchases := []simulator.Purchase{
		{Request: simulator.Request{EventTime: start}, AccountEvents: []simulator.AccountEvent{
			{EventID: "reset", EventTime: start.Add(-time.Hour)},
			{EventID: "login", EventTime: start.Add(-5 * time.Minute)},
		}},
		{Request: simulator.Request{EventTime: start.Add(time.Hour)}},
	}
	end := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)

	endAt(purchases, end)

	events := purchases[0].AccountEvents
	purchaseAt := purchases[0].Request.EventTime
	if !purchaseAt.Equal(end.Add(-time.Hour)) || !events[0].EventTime.Equal(purchaseAt.Add(-time.Hour)) ||
		!events[1].EventTime.Equal(purchaseAt.Add(-5*time.Minute)) {
		t.Fatalf("purchase at %v with events %+v, want the events moved with it", purchaseAt, events)
	}
}

// accountClient scores nothing and records the account events it receives.
// It returns err for every event.
type accountClient struct {
	scoreFunc
	events []string
	err    error
}

func (c *accountClient) SendAccountEvent(_ context.Context, e simulator.AccountEvent) error {
	c.events = append(c.events, e.EventID)
	return c.err
}

// captureLogs sends the default logger's output to the returned buffer until
// the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

func TestTrackedScorerForwardsAccountEvents(t *testing.T) {
	client := &accountClient{}
	tracker := &trackedScorer{client: client, id: "sim-1", cancel: func() {}}

	if err := tracker.SendAccountEvent(context.Background(), simulator.AccountEvent{EventID: "evt-1"}); err != nil {
		t.Fatal(err)
	}

	if len(client.events) != 1 || client.events[0] != "evt-1" {
		t.Fatalf("client received %v, want evt-1", client.events)
	}
}

func TestTrackedScorerSkipsAccountEventsTheClientCannotSend(t *testing.T) {
	tracker := &trackedScorer{client: scoreFunc(nil), id: "sim-1", cancel: func() {}}

	if err := tracker.SendAccountEvent(context.Background(), simulator.AccountEvent{EventID: "evt-1"}); err != nil {
		t.Fatalf("err = %v, want the event skipped", err)
	}
}

func TestTrackedScorerLogsAndCountsFailedAccountEvents(t *testing.T) {
	logs := captureLogs(t)
	client := &accountClient{err: errors.New("account events unavailable")}
	tracker := &trackedScorer{client: client, id: "sim-7", cancel: func() {}}

	err := tracker.SendAccountEvent(context.Background(), simulator.AccountEvent{EventID: "evt-1"})
	_ = tracker.SendAccountEvent(context.Background(), simulator.AccountEvent{EventID: "evt-2"})
	tracker.reportAccountEventFailures("completed")

	if err == nil || tracker.failedAccountEvents != 2 {
		t.Fatalf("err = %v, failed = %d; want the error returned and 2 failures counted", err, tracker.failedAccountEvents)
	}
	out := logs.String()
	for _, want := range []string{
		`msg="simulation account event failed" run_id=sim-7 event_id=evt-1`,
		`msg="simulation run finished with failed account events" run_id=sim-7 status=completed failed_account_events=2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("logs do not contain %q:\n%s", want, out)
		}
	}
}

func TestARunWithoutFailedAccountEventsLogsNothing(t *testing.T) {
	logs := captureLogs(t)
	tracker := &trackedScorer{client: &accountClient{}, id: "sim-8", cancel: func() {}}

	if err := tracker.SendAccountEvent(context.Background(), simulator.AccountEvent{EventID: "evt-1"}); err != nil {
		t.Fatal(err)
	}
	tracker.reportAccountEventFailures("completed")

	if logs.Len() != 0 {
		t.Errorf("logged %q, want nothing", logs.String())
	}
}
