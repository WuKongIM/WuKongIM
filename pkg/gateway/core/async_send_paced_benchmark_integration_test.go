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
	benchmarkBurstSendRate     = 667
	benchmarkBurstSendSessions = 510
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

// BenchmarkSendExecutorLocalThreeNodeBurst2000QPS replays one worker's share
// of the local three-node cluster workload. The legacy route used only 510
// distinct sessions for 667 messages; the availability-aware route uses one
// distinct session per message without changing the one-second grant cadence.
func BenchmarkSendExecutorLocalThreeNodeBurst2000QPS(b *testing.B) {
	for _, tc := range []struct {
		name     string
		sessions int
		records  int
	}{
		{name: "legacy_reused_sender", sessions: benchmarkBurstSendSessions, records: 1},
		{name: "distinct_sender", sessions: benchmarkBurstSendRate, records: 1},
		{name: "legacy_reused_sender_batch_128", sessions: benchmarkBurstSendSessions, records: 128},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkBurstSendExecutor(b, tc.sessions, tc.records)
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

func benchmarkBurstSendExecutor(b *testing.B, sessions, batchRecords int) {
	b.Helper()
	latencies := make([]time.Duration, b.N)
	waits := make([]time.Duration, b.N)
	handler := &pacedSendBenchmarkHandler{
		latencies: latencies, waits: waits, latency: measuredLocalThreeNodeSendHandlerLatency,
	}
	srv := benchmarkCoreServer(handler, gatewaytypes.SessionOptions{
		AsyncSendBatchMaxWait:    time.Millisecond,
		AsyncSendBatchMaxRecords: batchRecords,
		AsyncSendBatchMaxBytes:   512 * 1024,
	}, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1000,
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

	states := benchmarkCoreSessionStates(srv, sessions)
	handler.expect(b.N)
	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for index := range b.N {
		burst := index / benchmarkBurstSendRate
		pacedSendBenchmarkWaitUntil(started.Add(time.Duration(burst) * time.Second))
		send := &frame.SendPacket{
			ClientSeq:   uint64(index + 1),
			ClientMsgNo: fmt.Sprintf("burst-%d", index+1),
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

	reportSendLatency(b, latencies, 400*time.Millisecond)
	reportSendWait(b, waits)
}

type pacedSendBenchmarkHandler struct {
	wg        sync.WaitGroup
	latencies []time.Duration
	waits     []time.Duration
	latency   func(int) time.Duration
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
	startedAt := time.Now()
	for _, item := range items {
		itemIndex := int(item.Frame.ClientSeq) - 1
		if itemIndex >= 0 && itemIndex < len(h.waits) {
			h.waits[itemIndex] = startedAt.Sub(item.EnqueuedAt)
		}
	}
	latency := h.latency
	if latency == nil {
		latency = measuredThreeNodeSendHandlerLatency
	}
	time.Sleep(latency(index))
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

func reportSendWait(b *testing.B, waits []time.Duration) {
	b.Helper()
	if len(waits) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), waits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[(len(sorted)*99-1)/100]
	b.ReportMetric(float64(p99)/float64(time.Millisecond), "dispatch-wait-p99-ms")
}

// measuredLocalThreeNodeSendHandlerLatency deterministically samples the
// retained 2000 QPS SEND handler histogram from the failed local run. It has a
// 112.7ms mean and a roughly 307ms p99 inside the 250..500ms bucket.
func measuredLocalThreeNodeSendHandlerLatency(index int) time.Duration {
	rank := (uint64(index+1) * 11400714819323198485) % 603334
	switch {
	case rank < 8:
		return 5 * time.Millisecond
	case rank < 7800:
		return 17500 * time.Microsecond
	case rank < 70905:
		return 37500 * time.Microsecond
	case rank < 271867:
		return 75 * time.Millisecond
	case rank < 464170:
		return 125 * time.Millisecond
	case rank < 559028:
		return 175 * time.Millisecond
	case rank < 595537:
		return 225 * time.Millisecond
	default:
		return 375 * time.Millisecond
	}
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
	reportSendLatency(b, latencies, 200*time.Millisecond)
}

func reportSendLatency(b *testing.B, latencies []time.Duration, budget time.Duration) {
	b.Helper()
	if len(latencies) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[(len(sorted)*99-1)/100]
	aboveBudget := 0
	for _, latency := range latencies {
		if latency > budget {
			aboveBudget++
		}
	}
	ratio := 100 * float64(aboveBudget) / float64(len(latencies))
	b.ReportMetric(float64(p99)/float64(time.Millisecond), "send-p99-ms")
	b.ReportMetric(ratio, "send-over-budget-pct")
	if len(latencies) >= 1000 && ratio > 1 {
		b.Errorf("SEND operations above %s = %.3f%%, p99=%s; want <= 1%%", budget, ratio, p99)
	}
}

var _ gatewaytypes.SendBatchHandler = (*pacedSendBenchmarkHandler)(nil)
var _ gatewaytypes.Handler = (*pacedSendBenchmarkHandler)(nil)
