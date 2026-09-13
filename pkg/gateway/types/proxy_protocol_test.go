package types

import (
	"net/netip"
	"testing"
)

func TestProxyProtocolTrustRules(t *testing.T) {
	prefixes, err := ParseProxyProtocolTrustedCIDRs([]string{" 192.0.2.9/24 ", "2001:db8::/32", "::ffff:10.0.0.0/104"})
	if err != nil {
		t.Fatal(err)
	}
	for i, addr := range []string{"192.0.2.10", "2001:db8::1", "10.1.2.3"} {
		if !prefixes[i].Contains(netip.MustParseAddr(addr)) {
			t.Fatalf("%s does not contain %s", prefixes[i], addr)
		}
	}
	for _, values := range [][]string{{"proxy.example"}, {""}, {"127.0.0.1"}, {"0.0.0.0/33"}, {"::ffff:0:0/80"}, make([]string, 129)} {
		if _, err := ParseProxyProtocolTrustedCIDRs(values); err == nil {
			t.Fatalf("accepted %v", values)
		}
	}
	options := validGatewayOptionsContract()
	options.Listeners[0].ProxyProtocolTrustedCIDRs = []string{"not-a-cidr"}
	if err := options.Validate(); err == nil {
		t.Fatal("Options.Validate accepted invalid trust rule")
	}
}
