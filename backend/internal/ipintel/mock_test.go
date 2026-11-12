package ipintel

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMockClassifiesTheDefaultRanges(t *testing.T) {
	tests := []struct {
		addr string
		want Result
	}{
		{"192.0.2.10", Result{Provider: MockName, Available: true, Hosting: true}},
		{"192.0.2.127", Result{Provider: MockName, Available: true, Hosting: true}},
		{"192.0.2.128", Result{Provider: MockName, Available: true, VPN: true}},
		{"192.0.2.200", Result{Provider: MockName, Available: true, Proxy: true}},
		{"192.0.2.255", Result{Provider: MockName, Available: true, Tor: true}},
		{"::ffff:192.0.2.130", Result{Provider: MockName, Available: true, VPN: true}},
		{"2001:db8:d0::1", Result{Provider: MockName, Available: true, Hosting: true}},
		{"2001:db8:a0:ffff::1", Result{Provider: MockName, Available: true, VPN: true}},
		{"2001:db8:b0::abcd", Result{Provider: MockName, Available: true, Proxy: true}},
		{"2001:db8:c0::2", Result{Provider: MockName, Available: true, Tor: true}},
		{"2001:db8::1", Result{Provider: MockName, Available: true}},
		{"198.51.100.7", Result{Provider: MockName, Available: true}},
		{"203.0.113.9", Result{Provider: MockName, Available: true}},
	}
	mock := NewMock(DefaultRanges)
	for _, tt := range tests {
		got, err := mock.Lookup(context.Background(), netip.MustParseAddr(tt.addr))
		if err != nil || got != tt.want {
			t.Errorf("Lookup(%s) = %+v, %v; want %+v", tt.addr, got, err, tt.want)
		}
	}
}

func TestDefaultRangesStayInDocumentationRanges(t *testing.T) {
	documentation := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	for _, r := range DefaultRanges {
		inside := false
		for _, d := range documentation {
			inside = inside || (d.Contains(r.Prefix.Addr()) && r.Prefix.Bits() >= d.Bits())
		}
		if !inside {
			t.Errorf("range %s is outside the documentation ranges", r.Prefix)
		}
	}
}

func TestMockReportsEveryKindOfOverlappingRanges(t *testing.T) {
	mock := NewMock([]Range{
		{netip.MustParsePrefix("10.0.0.0/8"), KindHosting},
		{netip.MustParsePrefix("10.1.0.0/16"), KindVPN},
	})

	got, err := mock.Lookup(context.Background(), netip.MustParseAddr("10.1.2.3"))

	want := Result{Provider: MockName, Available: true, Hosting: true, VPN: true}
	if err != nil || got != want {
		t.Errorf("Lookup = %+v, %v; want %+v", got, err, want)
	}
}

func TestMockRejectsAnInvalidAddressAndAStoppedContext(t *testing.T) {
	mock := NewMock(DefaultRanges)
	if _, err := mock.Lookup(context.Background(), netip.Addr{}); err == nil {
		t.Error("Lookup of the zero address returned no error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mock.Lookup(ctx, netip.MustParseAddr("192.0.2.1")); err == nil {
		t.Error("Lookup with a cancelled context returned no error")
	}
}

func TestParseRanges(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    []Range
		wantErr bool
	}{
		{"IPv4 and IPv6", `[{"cidr":"10.0.0.0/8","kind":"hosting"},{"cidr":"fd00::1/64","kind":"tor"}]`,
			[]Range{{netip.MustParsePrefix("10.0.0.0/8"), KindHosting}, {netip.MustParsePrefix("fd00::/64"), KindTor}}, false},
		{"malformed CIDR", `[{"cidr":"10.0.0.300/8","kind":"hosting"}]`, nil, true},
		{"address without length", `[{"cidr":"10.0.0.1","kind":"vpn"}]`, nil, true},
		{"IPv4-mapped IPv6 range", `[{"cidr":"::ffff:10.0.0.0/104","kind":"hosting"}]`, nil, true},
		{"unknown kind", `[{"cidr":"10.0.0.0/8","kind":"residential"}]`, nil, true},
		{"empty list", `[]`, nil, true},
		{"not JSON", `10.0.0.0/8,hosting`, nil, true},
	}
	for _, tt := range tests {
		got, err := ParseRanges([]byte(tt.data))
		if (err != nil) != tt.wantErr || len(got) != len(tt.want) {
			t.Errorf("%s: ParseRanges = %v, %v; want %v, error %v", tt.name, got, err, tt.want, tt.wantErr)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%s: range %d = %v, want %v", tt.name, i, got[i], tt.want[i])
			}
		}
	}
}

func TestParseRangesExplainsAnIPv4MappedRange(t *testing.T) {
	_, err := ParseRanges([]byte(`[{"cidr":"::ffff:10.0.0.0/104","kind":"hosting"}]`))

	if err == nil || !strings.Contains(err.Error(), "IPv4 form") {
		t.Errorf("ParseRanges error = %v, want it to ask for the IPv4 form", err)
	}
}

func TestLoadRangesReadsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ranges.json")
	if err := os.WriteFile(path, []byte(`[{"cidr":"172.16.0.0/12","kind":"proxy"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadRanges(path)

	if err != nil || len(got) != 1 || got[0].Kind != KindProxy {
		t.Errorf("LoadRanges = %v, %v; want one proxy range", got, err)
	}
	if _, err := LoadRanges(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("LoadRanges of a missing file returned no error")
	}
}
