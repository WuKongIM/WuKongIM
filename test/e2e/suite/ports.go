//go:build e2e

package suite

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// PortSet groups the external listener addresses reserved for one e2e node.
type PortSet struct {
	ClusterAddr string
	GatewayAddr string
	APIAddr     string
	ManagerAddr string
}

// ReserveLoopbackPorts reserves distinct loopback listener addresses for one node.
func ReserveLoopbackPorts(t *testing.T) PortSet {
	t.Helper()

	return PortSet{
		ClusterAddr: reserveLoopbackAddr(t),
		GatewayAddr: reserveLoopbackAddr(t),
		APIAddr:     reserveLoopbackAddr(t),
		ManagerAddr: reserveLoopbackAddr(t),
	}
}

func reserveLoopbackAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}
