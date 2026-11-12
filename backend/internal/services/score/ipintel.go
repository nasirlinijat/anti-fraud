package score

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// IPIntel classifies the IP address of a purchase, such as *ipintel.Mock.
type IPIntel interface {
	Name() string
	Lookup(ctx context.Context, addr netip.Addr) (ipintel.Result, error)
}

// ipLookupTimeout bounds the IP intelligence lookup. Reputation services
// usually answer in a few milliseconds; a slower answer is dropped so that
// scoring stays within its latency budget, and the purchase is scored as if
// the address were unknown.
const ipLookupTimeout = 40 * time.Millisecond

// UseIPIntel makes the service look up the IP address of each new purchase
// with the provider. Without a provider no lookup is made.
func (s *Service) UseIPIntel(p IPIntel) {
	s.ipIntel = p
}

type lookupOutcome struct {
	result ipintel.Result
	err    error
}

// lookupIP returns the provider's result for the purchase's IP address, or nil
// when no lookup is made. A failed or late lookup never fails the score: it
// is logged and saved as unavailable, and the rules see an unknown address.
// The lookup runs before any database transaction, so a slow provider holds
// no database connection.
func (s *Service) lookupIP(ctx context.Context, p models.Purchase) *ipintel.Result {
	if s.ipIntel == nil || p.IPAddress == nil {
		return nil
	}
	name := s.ipIntel.Name()
	outcome := s.boundedLookup(ctx, *p.IPAddress)
	if outcome.err != nil {
		logLookupFailure(ctx, name, p.TransactionID, outcome.err)
		unavailable := ipintel.Unavailable(name)
		return &unavailable
	}
	outcome.result.Provider, outcome.result.Available = name, true
	return &outcome.result
}

// boundedLookup returns by the lookup deadline even when the provider ignores
// its context, and turns a provider panic into an error so that it cannot
// stop the server.
func (s *Service) boundedLookup(ctx context.Context, addr netip.Addr) lookupOutcome {
	lookupCtx, cancel := context.WithTimeout(ctx, ipLookupTimeout)
	defer cancel()
	// The buffered channel lets a late provider finish without blocking after
	// the score has moved on.
	done := make(chan lookupOutcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- lookupOutcome{err: fmt.Errorf("IP intelligence provider panicked: %v", r)}
			}
		}()
		result, err := s.ipIntel.Lookup(lookupCtx, addr)
		done <- lookupOutcome{result, err}
	}()
	select {
	case outcome := <-done:
		return outcome
	case <-lookupCtx.Done():
		return lookupOutcome{err: lookupCtx.Err()}
	}
}

// logLookupFailure warns about a provider that failed or answered late. A
// request that ended first is not the provider's fault, so it is not logged:
// the score fails on its own when it next uses the ended context.
func logLookupFailure(ctx context.Context, provider, transactionID string, err error) {
	if ctx.Err() != nil {
		return
	}
	msg := "IP intelligence lookup failed, scoring without it"
	if errors.Is(err, context.DeadlineExceeded) {
		msg = "IP intelligence lookup timed out, scoring without it"
	}
	slog.WarnContext(ctx, msg, "provider", provider, "transaction_id", transactionID, "error", err)
}

// ipReputation is the rule input for a lookup result. An unavailable or
// missing lookup gives the zero value, so the IP rules do not fire.
func ipReputation(r *ipintel.Result) scoring.IPReputation {
	if r == nil || !r.Available {
		return scoring.IPReputation{}
	}
	return scoring.IPReputation{Provider: r.Provider, Hosting: r.Hosting, VPN: r.VPN, Proxy: r.Proxy, Tor: r.Tor}
}
