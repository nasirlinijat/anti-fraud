package labels_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

// accountClient refuses purchases and records the account events it receives.
type accountClient struct {
	events []string
}

func (c *accountClient) Score(context.Context, simulator.Request) (simulator.Result, error) {
	return simulator.Result{}, errors.New("unexpected purchase")
}

func (c *accountClient) SendAccountEvent(_ context.Context, e simulator.AccountEvent) error {
	c.events = append(c.events, e.EventID)
	return nil
}

func TestScorerForwardsAccountEventsWithoutLabels(t *testing.T) {
	client := &accountClient{}
	scorer := &labels.Scorer{Client: client, RunID: "run-1"}

	if err := scorer.SendAccountEvent(context.Background(), simulator.AccountEvent{EventID: "evt-1"}); err != nil {
		t.Fatal(err)
	}

	if len(client.events) != 1 || client.events[0] != "evt-1" {
		t.Fatalf("client received %v, want evt-1", client.events)
	}
}
