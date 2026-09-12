package types

import (
	"fmt"
	"net/netip"
	"strings"
)

// ParseProxyProtocolTrustedCIDRs validates and snapshots listener trust rules at
// configuration/build time. Prefix count is bounded; matching never resolves DNS.
func ParseProxyProtocolTrustedCIDRs(values []string) ([]netip.Prefix, error) {
	if len(values) > 128 {
		return nil, fmt.Errorf("proxy_protocol_trusted_cidrs: at most 128 CIDRs are allowed")
	}
	var prefixes []netip.Prefix
	for _, value := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("proxy_protocol_trusted_cidrs: invalid CIDR %q", value)
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return nil, fmt.Errorf("proxy_protocol_trusted_cidrs: mapped IPv4 CIDR %q must have at least 96 prefix bits", value)
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}
