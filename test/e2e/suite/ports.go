//go:build e2e

package suite

import (
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	loopbackPortStart = 20000
	loopbackPortCount = 10000
)

var loopbackPortCursor atomic.Uint32

func init() {
	loopbackPortCursor.Store(uint32(os.Getpid() % loopbackPortCount))
}

// PortSet groups the external listener addresses reserved for one e2e node.
type PortSet struct {
	ClusterAddr string
	GatewayAddr string
	APIAddr     string
	ManagerAddr string
}

// ReserveLoopbackPorts reserves distinct loopback listener addresses for one node.
func ReserveLoopbackPorts(t testing.TB) PortSet {
	t.Helper()

	return PortSet{
		ClusterAddr: reserveLoopbackAddr(t),
		GatewayAddr: reserveLoopbackAddr(t),
		APIAddr:     reserveLoopbackAddr(t),
		ManagerAddr: reserveLoopbackAddr(t),
	}
}

func reserveLoopbackAddr(t testing.TB) string {
	t.Helper()

	var lastErr error
	for range loopbackPortCount {
		port := loopbackPortStart + int(loopbackPortCursor.Add(1)-1)%loopbackPortCount
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			lastErr = err
			continue
		}
		require.NoError(t, ln.Close())
		return addr
	}
	t.Fatalf("reserve loopback address: exhausted %d ports: %v", loopbackPortCount, lastErr)
	return ""
}
