//go:build integration

package core

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	benchmarkPacedSendRate     = 1000
	benchmarkPacedSendSessions = 2500
)

// BenchmarkSendExecutorPaced1000QPS replays the measured three-node SEND
// handler latency distribution through the real session-sharded executor.
// Run with -benchtime=5000x so the five-second sample covers enough shard
// collisions to make the one-percent 200ms budget meaningful.
func BenchmarkSendExecutorPaced1000QPS(b *testing.B) {
	defaults := gatewaytypes.NormalizeRuntimeOptions(gatewaytypes.RuntimeOptions{})
	for _, tc := range []struct {
		name    string
		workers int
	}{
		{name: "default", workers: defaults.AsyncSendWorkers},
		{name: "workers_256", workers: 256},
		{name: "workers_512", workers: 512},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkPacedSendExecutor(b, tc.workers)
		})
	}
}

func benchmarkPacedSendExecutor(b *testing.B, workers int) {
	b.Helper()
	latencies := make([]time.Duration, b.N)
	handler := &pacedSendBenchmarkHandler{latencies: latencies}
	srv := benchmarkCoreServer(handler, gatewaytypes.SessionOptions{
		AsyncSendBatchMaxWait:    time.Millisecond,
		AsyncSendBatchMaxRecords: 128,
		AsyncSendBatchMaxBytes:   512 * 1024,
	}, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        workers,
		AsyncSendQueueCapacity:  128 * 1024,
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  1,
		AsyncPoolReleaseTimeout: 5 * time.Second,
	})
	executor, err := newSendExecutor(srv, srv.options.Runtime)
	if err != nil {
		b.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	states := benchmarkCoreSessionStates(srv, benchmarkPacedSendSessions)
	handler.expect(b.N)
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for index := range b.N {
		pacedSendBenchmarkWaitUntil(started.Add(time.Duration(index) * time.Second / benchmarkPacedSendRate))
		send := &frame.SendPacket{
			ClientSeq:   uint64(index + 1),
			ClientMsgNo: fmt.Sprintf("paced-%d", index+1),
			ChannelID:   fmt.Sprintf("channel-%d", index%2000),
			ChannelType: 1,
			Payload:     make([]byte, 1024),
		}
		if !executor.submit(states[index%len(states)], "", send) {
			b.Fatalf("send submit rejected at iteration %d", index)
		}
	}
	handler.wait()
	b.StopTimer()

	reportPacedSendLatency(b, latencies)
}

type pacedSendBenchmarkHandler struct {
	wg        sync.WaitGroup
	latencies []time.Duration
}

func (h *pacedSendBenchmarkHandler) expect(count int)              { h.wg.Add(count) }
func (h *pacedSendBenchmarkHandler) wait()                         { h.wg.Wait() }
func (h *pacedSendBenchmarkHandler) OnListenerError(string, error) {}
func (h *pacedSendBenchmarkHandler) OnSessionOpen(gatewaytypes.Context) error {
	return nil
}
func (h *pacedSendBenchmarkHandler) OnFrame(gatewaytypes.Context, frame.Frame) error {
	return nil
}
func (h *pacedSendBenchmarkHandler) OnSessionClose(gatewaytypes.Context) error  { return nil }
func (h *pacedSendBenchmarkHandler) OnSessionError(gatewaytypes.Context, error) {}

func (h *pacedSendBenchmarkHandler) OnSendBatch(items []gatewaytypes.SendBatchItem) error {
	if len(items) == 0 {
		return nil
	}
	index := int(items[0].Frame.ClientSeq) - 1
	time.Sleep(measuredThreeNodeSendHandlerLatency(index))
	completedAt := time.Now()
	for _, item := range items {
		itemIndex := int(item.Frame.ClientSeq) - 1
		if itemIndex >= 0 && itemIndex < len(h.latencies) {
			h.latencies[itemIndex] = completedAt.Sub(item.EnqueuedAt)
		}
		h.wg.Done()
	}
	return nil
}

// measuredThreeNodeSendHandlerLatency deterministically samples the interval
// midpoints of the retained 1000 QPS SEND handler histogram. The distribution
// has a 94.7ms mean and a 200ms p99 bucket without embedding message identity.
func measuredThreeNodeSendHandlerLatency(index int) time.Duration {
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

func pacedSendBenchmarkWaitUntil(deadline time.Time) {
	if remaining := time.Until(deadline); remaining > 0 {
		timer := time.NewTimer(remaining)
		<-timer.C
	}
}

func reportPacedSendLatency(b *testing.B, latencies []time.Duration) {
	b.Helper()
	if len(latencies) == 0 {
		return
	}
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
	b.ReportMetric(float64(p99)/float64(time.Millisecond), "send-p99-ms")
	b.ReportMetric(ratio, "send-over-200ms-pct")
	if len(latencies) >= 1000 && ratio > 1 {
		b.Errorf("SEND operations above 200ms = %.3f%%, p99=%s; want <= 1%%", ratio, p99)
	}
}

var _ gatewaytypes.SendBatchHandler = (*pacedSendBenchmarkHandler)(nil)
var _ gatewaytypes.Handler = (*pacedSendBenchmarkHandler)(nil)
