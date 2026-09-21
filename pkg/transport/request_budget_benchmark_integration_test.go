//go:build integration

package transport_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/testkit"
)

// BenchmarkTransportBudget isolates negotiation from service queue/execution
// budgets with the same warmed TCP connections and fixed concurrent callers.
// Allocations include both endpoints; ns/op is inverse throughput, not RTT.
func BenchmarkTransportBudget(b *testing.B) {
	for _, size := range []int{64, 65537} {
		for _, workers := range []int{1, 16} {
			for _, budgets := range []bool{false, true} {
				b.Run(fmt.Sprintf("Bytes%d/Workers%d/Budgets%t", size, workers, budgets), func(b *testing.B) {
					s, err := transport.NewServer(transport.ServerConfig{NodeID: 2})
					if err != nil {
						b.Fatal(err)
					}
					defer s.Stop()
					err = s.Handle(1, func(_ context.Context, p []byte) ([]byte, error) { return p, nil }, transport.ServiceOptions{Concurrency: 64, QueueSize: 4096, MaxQueueBytes: 64 << 20, QueueTimeout: 5 * time.Second, Timeout: 30 * time.Second})
					if err != nil {
						b.Fatal(err)
					}
					if err = s.ListenAndServe("127.0.0.1:0"); err != nil {
						b.Fatal(err)
					}
					c, err := transport.NewClient(transport.ClientConfig{NodeID: 1, Discovery: testkit.StaticDiscovery{2: s.Addr()}, PoolSize: 16, RequestBudgets: budgets})
					if err != nil {
						b.Fatal(err)
					}
					defer c.Stop()
					payload := bytes.Repeat([]byte{0x5a}, size)
					call := func(shard int) error {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						got, err := c.Call(ctx, 2, uint64(shard), transport.PriorityRPC, 1, payload)
						if err == nil && !bytes.Equal(got, payload) {
							return fmt.Errorf("payload mismatch")
						}
						return err
					}
					for i := 0; i < 16; i++ {
						if err := call(i); err != nil {
							b.Fatal(err)
						}
					}
					var wg sync.WaitGroup
					var failures atomic.Int64
					b.ReportAllocs()
					b.SetBytes(int64(size))
					b.ResetTimer()
					for w := 0; w < workers; w++ {
						wg.Add(1)
						go func(w int) {
							defer wg.Done()
							for i := w; i < b.N; i += workers {
								if call(w) != nil {
									failures.Add(1)
								}
							}
						}(w)
					}
					wg.Wait()
					b.StopTimer()
					if failures.Load() != 0 {
						b.Fatalf("failed calls: %d", failures.Load())
					}
				})
			}
		}
	}
}
