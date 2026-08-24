//go:build integration

package app

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	gatewaypkg "github.com/WuKongIM/WuKongIM/pkg/gateway"
)

const (
	topCollectorPacedBenchmarkRate    = 333
	topCollectorPacedBenchmarkWorkers = 128
)

// BenchmarkTopCollectorPacedHotPath measures whether periodic top snapshots
// stall synchronous production observers at one node's share of 1000 QPS.
func BenchmarkTopCollectorPacedHotPath(b *testing.B) {
	for _, sampling := range []bool{false, true} {
		name := "hot_observers_only"
		if sampling {
			name = "with_periodic_sampling"
		}
		b.Run(name, func(b *testing.B) {
			benchmarkTopCollectorPacedHotPath(b, sampling)
		})
	}
}

func benchmarkTopCollectorPacedHotPath(b *testing.B, sampling bool) {
	collector := newTopCollector(topCollectorOptions{
		CollectInterval: time.Second,
		HistoryWindow:   5 * time.Minute,
		ResourceSampler: func() topResourceSample {
			return topResourceSample{}
		},
	})
	gatewayObserver := topGatewayObserver{top: collector}
	channelObserver := topChannelObserver{top: collector}
	seedTopCollectorPacedPressure(channelObserver)

	var cancel context.CancelFunc
	if sampling {
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		if err := collector.Start(ctx); err != nil {
			b.Fatalf("top collector Start(): %v", err)
		}
		defer func() {
			cancel()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			if err := collector.Stop(stopCtx); err != nil {
				b.Errorf("top collector Stop(): %v", err)
			}
		}()
	}

	latencies := make([]time.Duration, b.N)
	jobs := make(chan int, topCollectorPacedBenchmarkWorkers)
	var workers sync.WaitGroup
	workers.Add(topCollectorPacedBenchmarkWorkers)
	for range topCollectorPacedBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				started := time.Now()
				gatewayObserver.OnFrameIn(gatewaypkg.FrameEvent{FrameType: "SEND", Bytes: 1024})
				gatewayObserver.OnAsyncSendQueue(gatewaypkg.AsyncSendQueueEvent{Depth: 1, Capacity: 128 * 1024})
				pool := fmt.Sprintf("reactor_%d", index%256)
				channelObserver.ObserveWorkerWait(pool, worker.TaskQuorumCommit, 2*time.Millisecond)
				channelObserver.ObserveWorkerTask(pool, worker.TaskQuorumCommit, nil, 50*time.Millisecond)
				channelObserver.ObserveAppendBatch(1, 1024, time.Millisecond)
				channelObserver.ObserveAppendLatency(ch.CommitModeQuorum, 50*time.Millisecond)
				channelObserver.ObserveChannelAppendStage("runtime_append", "ok", 50*time.Millisecond)
				time.Sleep(topCollectorPacedHandlerLatency(index))
				gatewayObserver.OnAsyncSendDispatchWait(gatewaypkg.AsyncSendDispatchWaitEvent{Duration: 5 * time.Millisecond})
				gatewayObserver.OnAsyncSendQueue(gatewaypkg.AsyncSendQueueEvent{Depth: 0, Capacity: 128 * 1024})
				collector.ObserveGatewaySendack("success", "batch_result", "none")
				latencies[index] = time.Since(started)
			}
		}()
	}

	b.ResetTimer()
	started := time.Now()
	for index := range b.N {
		topCollectorPacedWaitUntil(started.Add(time.Duration(index) * time.Second / topCollectorPacedBenchmarkRate))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()

	reportTopCollectorPacedLatency(b, latencies)
}

func seedTopCollectorPacedPressure(observer topChannelObserver) {
	for reactorID := range 256 {
		pool := fmt.Sprintf("reactor_%d", reactorID)
		observer.SetReactorMailboxDepth(reactorID, "normal", 0)
		observer.SetReactorMailboxCapacity(reactorID, "normal", 1024)
		observer.SetWorkerQueueDepth(pool, 0)
		observer.SetWorkerQueueCapacity(pool, 1024)
		observer.SetWorkerWorkers(pool, 128)
		observer.SetWorkerInflight(pool, 0)
		for range 8 {
			observer.ObserveWorkerWait(pool, worker.TaskQuorumCommit, 2*time.Millisecond)
			observer.ObserveWorkerTask(pool, worker.TaskQuorumCommit, nil, 50*time.Millisecond)
		}
	}
}

func topCollectorPacedHandlerLatency(index int) time.Duration {
	rank := (uint64(index+1) * 11400714819323198485) % 59002
	switch {
	case rank < 4:
		return 5 * time.Millisecond
	case rank < 1223:
		return 17500 * time.Microsecond
	case rank < 7664:
		return 37500 * time.Microsecond
	case rank < 34539:
		return 75 * time.Millisecond
	case rank < 54184:
		return 125 * time.Millisecond
	case rank < 58828:
		return 175 * time.Millisecond
	case rank < 58980:
		return 225 * time.Millisecond
	default:
		return 375 * time.Millisecond
	}
}

func topCollectorPacedWaitUntil(deadline time.Time) {
	if remaining := time.Until(deadline); remaining > 0 {
		timer := time.NewTimer(remaining)
		<-timer.C
	}
}

func reportTopCollectorPacedLatency(b *testing.B, latencies []time.Duration) {
	b.Helper()
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[(len(sorted)*99-1)/100]
	aboveBudget := 0
	for _, latency := range latencies {
		if latency > 200*time.Millisecond {
			aboveBudget++
		}
	}
	ratio := 100 * float64(aboveBudget) / float64(len(latencies))
	b.Logf("hot observation latency: p99=%s over_200ms=%.3f%%", p99, ratio)
	b.ReportMetric(float64(p99)/float64(time.Millisecond), "hot-path-p99-ms")
	b.ReportMetric(ratio, "hot-path-over-200ms-pct")
}
