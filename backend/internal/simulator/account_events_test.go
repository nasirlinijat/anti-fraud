package simulator_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

func TestAccountEventsComeBeforeTheirPurchaseFromTheSamePlayer(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.5
	purchases := generate(t, cfg, 3000)

	ids := map[string]bool{}
	events := 0
	for _, p := range purchases {
		previous := time.Time{}
		for _, e := range p.AccountEvents {
			if e.PlayerID != p.Request.PlayerID || !e.EventTime.Before(p.Request.EventTime) || e.EventTime.Before(previous) {
				t.Fatalf("event %+v does not come before purchase %+v of its player, oldest first", e, p.Request)
			}
			if ids[e.EventID] {
				t.Fatalf("duplicate event ID %s", e.EventID)
			}
			ids[e.EventID] = true
			previous = e.EventTime
			events++
		}
	}
	if events == 0 {
		t.Fatal("no account events were generated")
	}
	if again := generate(t, cfg, 3000); !reflect.DeepEqual(purchases, again) {
		t.Fatal("the same config gave different account events")
	}
}

func TestTakeoversFollowALoginFromTheNewDevice(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.Scenarios = []simulator.Pattern{simulator.PatternAccountTakeover, simulator.PatternQuietTakeover}
	cfg.MissingFieldShare = 0
	purchases := generate(t, cfg, 400)

	resets := 0
	for _, p := range purchases {
		login := p.AccountEvents[len(p.AccountEvents)-1]
		if login.EventType != "login" || *login.DeviceID != *p.Request.DeviceID || *login.IPCountry != *p.Request.IPCountry ||
			p.Request.EventTime.Sub(login.EventTime) > 15*time.Minute {
			t.Fatalf("takeover %+v does not follow a login from its device and country: %+v", p.Request, p.AccountEvents)
		}
		if p.AccountEvents[0].EventType == "password_reset" {
			resets++
		}
	}
	if share := float64(resets) / float64(len(purchases)); share < 0.6 || share > 0.8 {
		t.Errorf("%.2f of takeovers start with a password reset, want about 0.7", share)
	}
}

func TestAccountEventsLeaveOutTheFieldsTheClientDoesNotSend(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.5
	for _, p := range generate(t, cfg, 3000) {
		for _, e := range p.AccountEvents {
			if (e.DeviceID == nil) != (p.Request.DeviceID == nil) || (e.IPCountry == nil) != (p.Request.IPCountry == nil) {
				t.Fatalf("event %+v and purchase %+v disagree on missing fields", e, p.Request)
			}
		}
	}
}

// recordingScorer records the order of the account events and purchases it
// receives.
type recordingScorer struct {
	mu    sync.Mutex
	calls []string
	fail  bool
}

func (r *recordingScorer) Score(_ context.Context, req simulator.Request) (simulator.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.TransactionID)
	return simulator.Result{Outcome: "allow"}, nil
}

func (r *recordingScorer) SendAccountEvent(_ context.Context, e simulator.AccountEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, e.EventID)
	if r.fail {
		return errors.New("account system unavailable")
	}
	return nil
}

func TestRunSendsAccountEventsJustBeforeTheirPurchase(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.SuspiciousShare = 0.5
	purchases := generate(t, cfg, 300)
	var want []string
	for _, p := range purchases {
		for _, e := range p.AccountEvents {
			want = append(want, e.EventID)
		}
		want = append(want, p.Request.TransactionID)
	}
	scorer := &recordingScorer{}

	sum, err := simulator.Run(t.Context(), scorer, purchases, 0, nil)

	if err != nil || sum.Sent != len(purchases) {
		t.Fatalf("sent %d, error %v", sum.Sent, err)
	}
	if !reflect.DeepEqual(scorer.calls, want) {
		t.Fatal("account events were not sent just before their purchases")
	}
}

func TestAFailedAccountEventIsReportedAndThePurchaseStillScored(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.Scenarios = []simulator.Pattern{simulator.PatternAccountTakeover}
	purchases := generate(t, cfg, 20)
	scorer := &recordingScorer{fail: true}
	var reported []error

	sum, err := simulator.Run(t.Context(), scorer, purchases, 0, func(err error) { reported = append(reported, err) })

	if err != nil || sum.Sent != len(purchases) || sum.Failed != 0 {
		t.Fatalf("sent %d, failed %d, error %v; want every purchase scored", sum.Sent, sum.Failed, err)
	}
	if len(reported) < len(purchases) {
		t.Fatalf("reported %d account event errors, want at least one per takeover", len(reported))
	}
}

// liveRecorder checks that live traffic keeps each account event as far
// before its purchase as in the backfill.
type liveRecorder struct {
	want   []simulator.Purchase
	cancel context.CancelFunc
	events []simulator.AccountEvent
	sent   int
	t      *testing.T
}

func (r *liveRecorder) SendAccountEvent(_ context.Context, e simulator.AccountEvent) error {
	r.events = append(r.events, e)
	return nil
}

func (r *liveRecorder) Score(_ context.Context, req simulator.Request) (simulator.Result, error) {
	expect := r.want[r.sent]
	if len(r.events) != len(expect.AccountEvents) {
		r.t.Fatalf("purchase %d came after %d account events, want %d", r.sent, len(r.events), len(expect.AccountEvents))
	}
	for i, e := range r.events {
		wantGap := expect.Request.EventTime.Sub(expect.AccountEvents[i].EventTime)
		if gap := req.EventTime.Sub(e.EventTime); gap != wantGap || e.EventID != expect.AccountEvents[i].EventID {
			r.t.Fatalf("event %s is %v before its live purchase, want %v", e.EventID, gap, wantGap)
		}
	}
	r.events = nil
	r.sent++
	if r.sent == len(r.want) {
		r.cancel()
	}
	return simulator.Result{Outcome: "allow"}, nil
}

func TestLiveMovesAccountEventsWithTheirPurchase(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.Start = time.Now().UTC()
	cfg.SuspiciousShare = 0.5
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	recorder := &liveRecorder{want: generate(t, cfg, 500), cancel: cancel, t: t}

	sum, err := simulator.RunLive(ctx, recorder, cfg, 0, nil)

	if !errors.Is(err, context.Canceled) || sum.Sent != 500 {
		t.Fatalf("sent %d, error %v", sum.Sent, err)
	}
}

// accountEvents keeps recorded account events in memory, like the store.
type accountEvents struct {
	mu    sync.Mutex
	saved map[string]models.AccountEvent
}

func (s *accountEvents) Record(_ context.Context, e models.AccountEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if saved, ok := s.saved[e.EventID]; ok {
		if !reflect.DeepEqual(saved, e) {
			return false, models.ErrDuplicateAccountEvent
		}
		return false, nil
	}
	s.saved[e.EventID] = e
	return true, nil
}

func TestClientSendsAccountEventsTheAPIAccepts(t *testing.T) {
	cfg := simulator.DefaultConfig()
	cfg.Start = time.Now().UTC().Add(-24 * time.Hour)
	cfg.Scenarios = []simulator.Pattern{simulator.PatternNormal, simulator.PatternAccountTakeover}
	purchases := generate(t, cfg, 200)
	srv, _, store := newServerWithAccountEvents(t)
	client := &simulator.Client{BaseURL: srv.URL, APIKey: testKey, HTTP: srv.Client()}
	var errs []error
	onError := func(err error) { errs = append(errs, err) }

	first, err := simulator.Run(t.Context(), client, purchases, 0, onError)
	if err != nil || first.Failed != 0 || len(errs) != 0 {
		t.Fatalf("first run failed %d, error %v, reported %v", first.Failed, err, errs)
	}
	// A repeated run sends the same events again, which the API accepts as retries.
	again, err := simulator.Run(t.Context(), client, purchases, 0, onError)
	if err != nil || again.Failed != 0 || len(errs) != 0 {
		t.Fatalf("repeated run failed %d, error %v, reported %v", again.Failed, err, errs)
	}

	want := 0
	for _, p := range purchases {
		want += len(p.AccountEvents)
	}
	if len(store.saved) != want || want == 0 {
		t.Fatalf("saved %d account events, want %d", len(store.saved), want)
	}
}
