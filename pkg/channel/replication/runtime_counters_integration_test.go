//go:build integration

package replication_test

import (
	"os"
	"runtime"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/counterwindow"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

// newAppendGateCounters enables bounded batch/stage histograms only for the
// fixed PR seam. Go's initial one-iteration calibration retains its old path.
func newAppendGateCounters(b *testing.B) *metrics.Registry {
	if os.Getenv("WK_BENCH_APPEND_COUNTERS_DIR") == "" {
		return nil
	}
	if b.Name() != "BenchmarkThreeNodeChannelAppend500QPS" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || runtime.GOMAXPROCS(0) != 4 || (b.N != 1 && b.N != 3000 && !(os.Getenv("WK_BENCH_QUALIFY") == "1" && b.N == 90000)) {
		b.Fatal("append counters require Linux amd64, GOMAXPROCS=4 and 3000 diagnostic or 90000 qualification operations in the 500 QPS seam")
	}
	if b.N == 1 {
		return nil
	}
	// One registry aggregates all three nodes; it must not imply per-node CPU.
	return metrics.New(0, "benchmark-cluster")
}

// startAppendGateCounterWindow excludes cluster setup and captures completion
// before any latency assertion or cluster close. It never owns the gate verdict.
func startAppendGateCounterWindow(b *testing.B, cluster *durableQuorumBenchmarkCluster, rate int) func() {
	if cluster.counters == nil {
		return func() {}
	}
	if rate != 500 {
		b.Fatal("append counters require the unchanged 500 QPS rate")
	}
	dir := os.Getenv("WK_BENCH_APPEND_COUNTERS_DIR")
	if err := os.Mkdir(dir, 0700); err != nil {
		b.Fatal(err)
	}
	return counterwindow.Start(b, dir, "channel-append-counters/v1",
		"post-setup through completed handlers; includes boundary snapshot and benchmark timer overhead; three-node aggregate; replication stages sampled 1/32; physical batches unsampled",
		b.N, rate, cluster.counters.PrometheusRegistry())
}
