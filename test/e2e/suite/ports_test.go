//go:build e2e

package suite

import (
	"net"
	"strconv"
	"testing"
)

func TestReserveLoopbackPortsReturnsDistinctNonEphemeralPorts(t *testing.T) {
	seen := make(map[string]struct{})
	for range 16 {
		ports := ReserveLoopbackPorts(t)
		for _, addr := range []string{ports.ClusterAddr, ports.GatewayAddr, ports.APIAddr, ports.ManagerAddr} {
			if _, ok := seen[addr]; ok {
				t.Fatalf("ReserveLoopbackPorts() reused %s", addr)
			}
			seen[addr] = struct{}{}
			_, rawPort, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("SplitHostPort(%q): %v", addr, err)
			}
			port, err := strconv.Atoi(rawPort)
			if err != nil {
				t.Fatalf("Atoi(%q): %v", rawPort, err)
			}
			if port < loopbackPortStart || port >= loopbackPortStart+loopbackPortCount {
				t.Fatalf("reserved port %d outside [%d,%d)", port, loopbackPortStart, loopbackPortStart+loopbackPortCount)
			}
		}
	}
}
