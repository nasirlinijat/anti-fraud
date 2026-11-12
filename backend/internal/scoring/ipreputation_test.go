package scoring

import "testing"

func TestIPReputationRules(t *testing.T) {
	tests := []struct {
		name       string
		reputation IPReputation
		want       [2]bool // datacenter_ip, anonymous_ip
	}{
		{"no lookup", IPReputation{}, [2]bool{false, false}},
		{"ordinary address", IPReputation{Provider: "mock"}, [2]bool{false, false}},
		{"hosting", IPReputation{Provider: "mock", Hosting: true}, [2]bool{true, false}},
		{"VPN", IPReputation{Provider: "mock", VPN: true}, [2]bool{false, true}},
		{"public proxy", IPReputation{Provider: "mock", Proxy: true}, [2]bool{false, true}},
		{"Tor", IPReputation{Provider: "mock", Tor: true}, [2]bool{false, true}},
		{"VPN in a data centre", IPReputation{Provider: "mock", Hosting: true, VPN: true}, [2]bool{true, true}},
	}
	for _, tt := range tests {
		in := Input{IPReputation: tt.reputation}
		if got := [2]bool{datacenterIP(in, RuleConfig{}), anonymousIP(in, RuleConfig{})}; got != tt.want {
			t.Errorf("%s: fired %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIPReputationMessagesNameTheKindAndProvider(t *testing.T) {
	tests := []struct {
		got, want string
	}{
		{datacenterIPMessage(Input{IPReputation: IPReputation{Provider: "mock", Hosting: true}}, RuleConfig{}),
			"IP address belongs to a hosting or data-centre network (mock)"},
		{anonymousIPMessage(Input{IPReputation: IPReputation{Provider: "mock", Tor: true}}, RuleConfig{}),
			"IP address is a Tor exit (mock)"},
		{anonymousIPMessage(Input{IPReputation: IPReputation{Provider: "mock", VPN: true, Proxy: true}}, RuleConfig{}),
			"IP address is a VPN and a public proxy (mock)"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("message = %q, want %q", tt.got, tt.want)
		}
	}
}
