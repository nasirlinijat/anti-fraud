package ipintel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
)

// MockName is the provider name the mock saves with its results.
const MockName = "mock"

// Kind is the kind of network a range belongs to.
type Kind string

const (
	KindHosting Kind = "hosting"
	KindVPN     Kind = "vpn"
	KindProxy   Kind = "proxy"
	KindTor     Kind = "tor"
)

// Range is one classified block of addresses.
type Range struct {
	Prefix netip.Prefix
	Kind   Kind
}

// DefaultRanges lie inside the documentation ranges of RFC 5737 (192.0.2.0/24)
// and RFC 3849 (2001:db8::/32), so they never classify a real address. The
// simulator places hosting, VPN, proxy, and Tor traffic in them.
var DefaultRanges = []Range{
	{netip.MustParsePrefix("192.0.2.0/25"), KindHosting},
	{netip.MustParsePrefix("192.0.2.128/26"), KindVPN},
	{netip.MustParsePrefix("192.0.2.192/27"), KindProxy},
	{netip.MustParsePrefix("192.0.2.224/27"), KindTor},
	{netip.MustParsePrefix("2001:db8:d0::/48"), KindHosting},
	{netip.MustParsePrefix("2001:db8:a0::/48"), KindVPN},
	{netip.MustParsePrefix("2001:db8:b0::/48"), KindProxy},
	{netip.MustParsePrefix("2001:db8:c0::/48"), KindTor},
}

// Mock classifies addresses by a fixed table of ranges. An address in no
// range is reported as an ordinary address.
type Mock struct {
	ranges []Range
}

// NewMock returns a mock that classifies addresses by the given ranges.
func NewMock(ranges []Range) *Mock {
	return &Mock{ranges: ranges}
}

// Name returns MockName, which is saved with every result of the mock.
func (m *Mock) Name() string { return MockName }

// Lookup reports every kind whose range contains the address. An IPv4 address
// written in IPv6 form is looked up as IPv4.
func (m *Mock) Lookup(ctx context.Context, addr netip.Addr) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if !addr.IsValid() {
		return Result{}, errors.New("look up IP address: the address is not valid")
	}
	addr = addr.Unmap()
	result := Result{Provider: MockName, Available: true}
	for _, r := range m.ranges {
		if r.Prefix.Contains(addr) {
			result.mark(r.Kind)
		}
	}
	return result, nil
}

func (r *Result) mark(kind Kind) {
	switch kind {
	case KindHosting:
		r.Hosting = true
	case KindVPN:
		r.VPN = true
	case KindProxy:
		r.Proxy = true
	case KindTor:
		r.Tor = true
	}
}

// rangeEntry is one entry of a ranges file.
type rangeEntry struct {
	CIDR string `json:"cidr"`
	Kind Kind   `json:"kind"`
}

// LoadRanges reads a JSON array of {"cidr": "...", "kind": "..."} entries,
// which replace the default ranges.
func LoadRanges(path string) ([]Range, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read IP ranges: %w", err)
	}
	return ParseRanges(data)
}

// ParseRanges parses the JSON format that LoadRanges reads.
func ParseRanges(data []byte) ([]Range, error) {
	var entries []rangeEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse IP ranges: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("parse IP ranges: the file lists no ranges")
	}
	ranges := make([]Range, 0, len(entries))
	for i, e := range entries {
		prefix, err := netip.ParsePrefix(e.CIDR)
		if err != nil {
			return nil, fmt.Errorf("parse IP range %d: %w", i+1, err)
		}
		// Lookup unmaps addresses, so an IPv4-mapped IPv6 range would never
		// match anything.
		if prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("parse IP range %d: write the IPv4-mapped range %s in IPv4 form", i+1, e.CIDR)
		}
		if !e.Kind.known() {
			return nil, fmt.Errorf("parse IP range %d: kind must be hosting, vpn, proxy, or tor", i+1)
		}
		ranges = append(ranges, Range{Prefix: prefix.Masked(), Kind: e.Kind})
	}
	return ranges, nil
}

func (k Kind) known() bool {
	switch k {
	case KindHosting, KindVPN, KindProxy, KindTor:
		return true
	}
	return false
}
