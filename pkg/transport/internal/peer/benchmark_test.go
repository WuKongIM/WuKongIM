package peer

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func BenchmarkManagerAcquireHotPath(b *testing.B) {
	cases := []struct {
		name     string
		poolSize int
	}{
		{name: "Pool1", poolSize: 1},
		{name: "Pool4", poolSize: 4},
		{name: "Pool8", poolSize: 8},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			var serversMu sync.Mutex
			var servers []net.Conn
			m := NewManager(Config{
				Discovery: testDiscovery{2: "node-2"},
				PoolSize:  tc.poolSize,
				Limits:    testLimits(),
				Dial: func(context.Context, string, string) (net.Conn, error) {
					client, server := net.Pipe()
					serversMu.Lock()
					servers = append(servers, server)
					serversMu.Unlock()
					return client, nil
				},
			})
			defer m.Stop()
			defer closeServers(&serversMu, &servers)

			for shardKey := 0; shardKey < tc.poolSize; shardKey++ {
				if _, err := m.Acquire(context.Background(), 2, uint64(shardKey)); err != nil {
					b.Fatalf("warm-up Acquire() error = %v", err)
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := m.Acquire(context.Background(), 2, uint64(i)); err != nil {
					b.Fatalf("Acquire() error = %v", err)
				}
			}
		})
	}
}

func BenchmarkManagerAcquireParallelMultiPeer(b *testing.B) {
	const poolSize = 4
	nodes := []core.NodeID{2, 3, 4}
	discovery := testDiscovery{
		2: "node-2",
		3: "node-3",
		4: "node-4",
	}
	var serversMu sync.Mutex
	var servers []net.Conn
	m := NewManager(Config{
		Discovery: discovery,
		PoolSize:  poolSize,
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer m.Stop()
	defer closeServers(&serversMu, &servers)

	for _, nodeID := range nodes {
		for shardKey := 0; shardKey < poolSize; shardKey++ {
			if _, err := m.Acquire(context.Background(), nodeID, uint64(shardKey)); err != nil {
				b.Fatalf("warm-up Acquire(node=%d, shard=%d) error = %v", nodeID, shardKey, err)
			}
		}
	}

	var calls atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			call := calls.Add(1)
			nodeID := nodes[int(call)%len(nodes)]
			if _, err := m.Acquire(context.Background(), nodeID, call); err != nil {
				b.Fatalf("Acquire() error = %v", err)
			}
		}
	})
}

func BenchmarkManagerClosePeerRedial(b *testing.B) {
	serverCh := make(chan net.Conn, 1)
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		PoolSize:  1,
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			serverCh <- server
			return client, nil
		},
	})
	defer m.Stop()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Acquire(context.Background(), 2, 0); err != nil {
			b.Fatalf("Acquire() error = %v", err)
		}
		m.ClosePeer(2)
		(<-serverCh).Close()
	}
}
