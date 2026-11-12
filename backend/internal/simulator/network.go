package simulator

import "fmt"

// Network kinds the mock IP intelligence provider reports.
const (
	networkHosting = "hosting"
	networkVPN     = "vpn"
	networkProxy   = "proxy"
	networkTor     = "tor"
)

// networkBlock is where the simulator places connections of one kind. The
// blocks are the default ranges of the mock IP intelligence provider, inside
// the documentation ranges 192.0.2.0/24 and 2001:db8::/32; a test checks that
// the mock classifies every placed address as its kind.
type networkBlock struct {
	firstHost, hosts int
	ipv6Prefix       string
}

var networkBlocks = map[string]networkBlock{
	networkHosting: {0, 128, "2001:db8:d0::"},
	networkVPN:     {128, 64, "2001:db8:a0::"},
	networkProxy:   {192, 32, "2001:db8:b0::"},
	networkTor:     {224, 32, "2001:db8:c0::"},
}

// ipv6Share is the share of placed connections that use IPv6.
const ipv6Share = 0.25

// legitimateNetworks are the shares of connections of a legitimate pattern
// that go through each kind of network: a few regular players always use a
// VPN, so the IP rules also catch innocent purchases.
var legitimateNetworks = []resultShare{{networkVPN, 0.03}}

// patternNetworks overrides legitimateNetworks. Travellers and expats use
// VPNs to reach home services, internet cafes often sit behind a proxy, bots
// run in data centres, and fraudsters hide their location behind VPNs,
// proxies, and Tor. Many fraudsters use residential connections that a
// reputation service cannot flag, so the shares stay well below one.
var patternNetworks = map[Pattern][]resultShare{
	PatternTraveler:        {{networkVPN, 0.20}, {networkProxy, 0.03}},
	PatternInternetCafe:    {{networkProxy, 0.20}, {networkHosting, 0.05}},
	PatternExpat:           {{networkVPN, 0.15}},
	PatternCardTesting:     {{networkHosting, 0.45}, {networkProxy, 0.15}, {networkTor, 0.05}},
	PatternDeviceFarm:      {{networkHosting, 0.50}, {networkProxy, 0.10}},
	PatternSlowCardTesting: {{networkHosting, 0.25}, {networkVPN, 0.15}},
	PatternStolenCard:      {{networkVPN, 0.25}, {networkProxy, 0.10}, {networkTor, 0.05}},
	PatternAccountTakeover: {{networkVPN, 0.30}, {networkProxy, 0.10}, {networkTor, 0.05}},
	PatternQuietTakeover:   {{networkVPN, 0.10}},
	PatternNewAccountSpend: {{networkVPN, 0.20}, {networkHosting, 0.10}},
	PatternLowAndSlow:      {{networkVPN, 0.05}},
}

// withNetwork moves the purchase's connection into a hosting, VPN, proxy, or
// Tor range for the pattern's share of connections. The draws are keyed by the
// original address alone, outside the random sequence: a shared connection,
// such as a family home, an internet cafe, or a device farm, moves as a whole,
// a regular player's home is on a VPN for every legitimate purchase or for
// none, and the rest of the traffic is the same as without placement.
func (g *generator) withNetwork(pattern Pattern, req Request) Request {
	if req.IPAddress == nil {
		return req
	}
	shares, special := patternNetworks[pattern]
	if !special {
		shares = legitimateNetworks
	}
	home := *req.IPAddress
	kind := drawResult(g.seededDraw(home, "network"), shares, "")
	if kind == "" {
		return req
	}
	req.IPAddress = ptr(g.networkAddress(home, kind))
	return req
}

// networkAddress picks the address in the kind's block that stands in for the
// home address.
func (g *generator) networkAddress(home, kind string) string {
	block := networkBlocks[kind]
	host := g.seededDraw(home, "network_host")
	if g.seededDraw(home, "network_ipv6") < ipv6Share {
		return fmt.Sprintf("%s%x", block.ipv6Prefix, 1+int(host*0xfffe))
	}
	return fmt.Sprintf("192.0.2.%d", block.firstHost+int(host*float64(block.hosts)))
}
