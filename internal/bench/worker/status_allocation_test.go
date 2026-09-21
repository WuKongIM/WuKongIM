//go:build !race

package worker

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/stretchr/testify/require"
)

// Allocation invariants exclude race instrumentation, whose bookkeeping can
// allocate nondeterministically. Semantic/concurrency coverage runs with race.
func TestLifecycleStatusAllocationsIgnoreLatencyHistory(t *testing.T) {
	registry := metrics.NewRegistry()
	registry.IncCounter("sendack_success_total", metrics.Labels{"phase": "run"})
	person, err := benchworkload.NewPersonWorkload(benchworkload.PersonConfig{
		SenderUID: "u1", RecipientUID: "u2", Metrics: registry,
	}, map[string]benchworkload.PersonClient{"u1": &workerPersonClient{}, "u2": &workerPersonClient{}})
	require.NoError(t, err)
	runner := &defaultWorkloadRunner{personWorkloads: []*benchworkload.PersonWorkload{person}}
	poll := func() { _ = runner.LifecycleStatus() }
	emptyHistory := testing.AllocsPerRun(20, poll)
	for i := 0; i < 32; i++ {
		registry.ObserveLatency("sendack_latency_seconds", nil, time.Duration(32-i)*time.Millisecond)
	}
	withHistory := testing.AllocsPerRun(20, poll)
	require.Equal(t, emptyHistory, withHistory, "status must not allocate latency copies or summaries")
	require.Equal(t, uint64(32), runner.MetricsSnapshot().Histograms["sendack_latency_seconds"].Count)
}
