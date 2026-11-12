package scoring

import (
	"fmt"
	"strings"
)

// IPReputation is what the IP intelligence provider reported for the
// purchase's IP address. It is the zero value when no lookup was made or the
// lookup failed, so the rules that read it do not fire.
type IPReputation struct {
	Provider string
	Hosting  bool
	VPN      bool
	Proxy    bool
	Tor      bool
}

func (r IPReputation) anonymous() bool {
	return r.VPN || r.Proxy || r.Tor
}

func datacenterIP(in Input, _ RuleConfig) bool {
	return in.IPReputation.Hosting
}

func anonymousIP(in Input, _ RuleConfig) bool {
	return in.IPReputation.anonymous()
}

func datacenterIPMessage(in Input, _ RuleConfig) string {
	return fmt.Sprintf("IP address belongs to a hosting or data-centre network (%s)", in.IPReputation.Provider)
}

func anonymousIPMessage(in Input, _ RuleConfig) string {
	var kinds []string
	for _, k := range []struct {
		matched bool
		name    string
	}{{in.IPReputation.VPN, "a VPN"}, {in.IPReputation.Proxy, "a public proxy"}, {in.IPReputation.Tor, "a Tor exit"}} {
		if k.matched {
			kinds = append(kinds, k.name)
		}
	}
	return fmt.Sprintf("IP address is %s (%s)", strings.Join(kinds, " and "), in.IPReputation.Provider)
}
