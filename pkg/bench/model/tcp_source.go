package model

import (
	"fmt"
	"net/netip"
	"strings"
)

// ValidateTCPSourceConfig validates a complete explicit worker-local TCP source pool.
func ValidateTCPSourceConfig(cfg *TCPSourceConfig) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.IPv4Addrs) == 0 {
		return fmt.Errorf("ipv4_addrs must contain at least one address")
	}
	seen := make(map[netip.Addr]struct{}, len(cfg.IPv4Addrs))
	for idx, raw := range cfg.IPv4Addrs {
		addr, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || !addr.Is4() {
			return fmt.Errorf("ipv4_addrs[%d] must be a valid IPv4 address", idx)
		}
		if addr.IsUnspecified() {
			return fmt.Errorf("ipv4_addrs[%d] must not be unspecified", idx)
		}
		if _, ok := seen[addr]; ok {
			return fmt.Errorf("ipv4_addrs must be unique: %s", addr)
		}
		seen[addr] = struct{}{}
	}
	if cfg.PortMin < 1024 {
		return fmt.Errorf("port_min must be at least 1024")
	}
	if cfg.PortMax > 65535 {
		return fmt.Errorf("port_max must not exceed 65535")
	}
	if cfg.PortMin > cfg.PortMax {
		return fmt.Errorf("port_min must not exceed port_max")
	}
	return nil
}

// TCPSourceCapacity returns the number of unique address/port candidates in cfg.
// Callers must validate cfg before using the result.
func TCPSourceCapacity(cfg *TCPSourceConfig) int64 {
	if cfg == nil || len(cfg.IPv4Addrs) == 0 || cfg.PortMax < cfg.PortMin {
		return 0
	}
	return int64(len(cfg.IPv4Addrs)) * int64(cfg.PortMax-cfg.PortMin+1)
}
