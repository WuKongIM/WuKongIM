//go:build integration

package app

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/testkit"
)

// BenchmarkClusterRPCObservation compares real production metrics against the
// same negotiated transport path without an observer, at fixed CPU parallelism.
func BenchmarkClusterRPCObservation(b *testing.B) {
	for _, observed := range []bool{false, true} {
		for _, parallel := range []bool{false, true} {
			b.Run(fmt.Sprintf("metrics=%t/parallel=%t", observed, parallel), func(b *testing.B) {
				var clientObserver, serverObserver transport.Observer
				if observed {
					clientObserver = &transportMetricsObserver{metrics: obsmetrics.NewWithLogicalSlots(1, "client", 256)}
					serverObserver = &transportMetricsObserver{metrics: obsmetrics.NewWithLogicalSlots(2, "server", 256)}
				}
				server := clusternet.NewTransportServer(clusternet.TransportServerConfig{NodeID: 2, Observer: serverObserver})
				server.Register(clusternet.RPCChannelAuthoritySend, clusternet.HandlerFunc(func(_ context.Context, p []byte) ([]byte, error) { return p, nil }))
				if err := server.Start("127.0.0.1:0"); err != nil {
					b.Fatal(err)
				}
				defer server.Stop()
				client := clusternet.NewTransportClient(clusternet.TransportClientConfig{NodeID: 1, Observer: clientObserver, Discovery: testkit.StaticDiscovery{2: server.Addr()}})
				defer client.Stop()
				payload := make([]byte, 64)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				for i := 0; i < 16; i++ {
					if _, err := client.CallShard(ctx, 2, clusternet.RPCChannelAuthoritySend, uint64(i), payload); err != nil {
						b.Fatal(err)
					}
				}
				var sequence atomic.Uint64
				call := func() {
					if _, err := client.CallShard(ctx, 2, clusternet.RPCChannelAuthoritySend, sequence.Add(1), payload); err != nil {
						b.Error(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				if parallel {
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							call()
						}
					})
				} else {
					for i := 0; i < b.N; i++ {
						call()
					}
				}
				b.StopTimer()
			})
		}
	}
}
