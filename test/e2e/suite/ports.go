//go:build e2e

package suite

import (
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	loopbackPortStart      = 20000
	loopbackPortCount      = 10000
	loopbackPortBlockCount = 32
	loopbackPortBlockSize  = loopbackPortCount / loopbackPortBlockCount
)

var loopbackPortCursor atomic.Uint32

var processLoopbackPortLease struct {
	once sync.Once

	// sentinel remains open for the test process lifetime so other test
	// processes cannot claim this block.
	sentinel net.Listener
	start    int
	count    int
	err      error
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

	blockStart, blockCount, err := reserveProcessLoopbackPortBlock()
	require.NoError(t, err)
	var lastErr error
	for range blockCount {
		port := blockStart + int(loopbackPortCursor.Add(1)-1)%blockCount
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			lastErr = err
			continue
		}
		require.NoError(t, ln.Close())
		return addr
	}
	t.Fatalf("reserve loopback address: exhausted process block of %d ports: %v", blockCount, lastErr)
	return ""
}

func reserveProcessLoopbackPortBlock() (int, int, error) {
	processLoopbackPortLease.once.Do(func() {
		firstBlock := os.Getpid() % loopbackPortBlockCount
		var lastErr error
		for offset := range loopbackPortBlockCount {
			block := (firstBlock + offset) % loopbackPortBlockCount
			sentinelPort := loopbackPortStart + block*loopbackPortBlockSize
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", sentinelPort))
			if err != nil {
				lastErr = err
				continue
			}
			processLoopbackPortLease.sentinel = listener
			processLoopbackPortLease.start = sentinelPort + 1
			processLoopbackPortLease.count = loopbackPortBlockSize - 1
			return
		}
		processLoopbackPortLease.err = fmt.Errorf("reserve loopback port block: exhausted %d blocks: %w", loopbackPortBlockCount, lastErr)
	})
	return processLoopbackPortLease.start, processLoopbackPortLease.count, processLoopbackPortLease.err
}
