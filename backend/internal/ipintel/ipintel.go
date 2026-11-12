// Package ipintel classifies IP addresses as hosting, VPN, public proxy, or
// Tor addresses. A deployment would put a commercial IP reputation service
// behind Provider; Mock classifies addresses from a table of ranges.
package ipintel

import (
	"context"
	"net/netip"
)

// Provider looks up what is known about an IP address.
type Provider interface {
	// Name identifies the provider in saved results.
	Name() string
	// Lookup classifies the address. It returns an error when it cannot tell.
	// It must return promptly once ctx is done: the caller stops waiting at
	// its deadline, and a lookup that keeps running holds its goroutine and
	// connection until it ends.
	Lookup(ctx context.Context, addr netip.Addr) (Result, error)
}

// Result is what a provider reported for one address. It is saved with the
// decision as JSON, so its field names are part of the stored format.
type Result struct {
	Provider string `json:"provider"`
	// Available is false when the lookup failed or timed out. The flags are
	// then all false and no rule reads them.
	Available bool `json:"available"`
	Hosting   bool `json:"hosting"`
	VPN       bool `json:"vpn"`
	Proxy     bool `json:"proxy"`
	Tor       bool `json:"tor"`
}

// Unavailable is the result saved when the provider failed or timed out.
func Unavailable(provider string) Result {
	return Result{Provider: provider}
}
