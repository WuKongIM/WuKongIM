package worker

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
)

// BenchmarkLifecycleStatus measures the actual status projection with a fixed
// latency history. No clients are started and setup is outside the timed region.
func BenchmarkLifecycleStatus(b *testing.B) {
	for _, samples := range []int{0, 30000, 810000} {
		b.Run(fmt.Sprintf("Samples%d", samples), func(b *testing.B) {
			registry := metrics.NewRegistry()
			registry.AddCounter("sendack_success_total", metrics.Labels{"phase": "run"}, 123)
			rng := rand.New(rand.NewPCG(42, 17))
			for i := 0; i < samples; i++ {
				registry.ObserveLatency("group_recv_latency_seconds", nil, time.Duration(rng.Int64N(int64(time.Second))))
			}
			person, err := benchworkload.NewPersonWorkload(benchworkload.PersonConfig{
				SenderUID: "u1", RecipientUID: "u2", Metrics: registry,
			}, map[string]benchworkload.PersonClient{
				"u1": &workerPersonClient{}, "u2": &workerPersonClient{},
			})
			if err != nil {
				b.Fatal(err)
			}
			runner := &defaultWorkloadRunner{personWorkloads: []*benchworkload.PersonWorkload{person}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := runner.LifecycleStatus().Traffic.SendACKs; got != 123 {
					b.Fatalf("SENDACK count = %d, want 123", got)
				}
			}
		})
	}
}
