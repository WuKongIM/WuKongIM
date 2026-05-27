package channelv2_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/service"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

var benchPayload = []byte("hello-channelv2")

func BenchmarkAppendSingleNodeHotChannelBatched(b *testing.B) {
	obs := &benchmarkObserver{}
	cluster := newBenchSingleNodeWithObserver(b, obs)
	meta := benchMeta("hot-batched")
	if err := cluster.ApplyMeta(meta); err != nil {
		b.Fatal(err)
	}
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()

	runParallelAppends(b, cluster, []ch.ChannelID{meta.ID}, ch.CommitModeLocal)

	b.StopTimer()
	reportBenchmarkObserver(b, obs, startGoroutines)
}

func BenchmarkAppendSingleNodeManyChannelsAsync(b *testing.B) {
	obs := &benchmarkObserver{}
	cluster := newBenchSingleNodeWithObserver(b, obs)
	ids := precreateBenchMeta(b, cluster, 1024, benchMeta)
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()

	runParallelAppends(b, cluster, ids, ch.CommitModeLocal)

	b.StopTimer()
	reportBenchmarkObserver(b, obs, startGoroutines)
}

func BenchmarkAppendThreeNodeManyChannelsAsync(b *testing.B) {
	h := testkit.NewClusterHarness(b, []ch.NodeID{1, 2, 3})
	defer h.Close()
	ids := make([]ch.ChannelID, 128)
	for i := range ids {
		meta := benchMetaThreeNode(fmt.Sprintf("async-c-%d", i))
		h.ApplyMetaToAll(meta)
		ids[i] = meta.ID
	}
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()

	runParallelAppends(b, h.Nodes[1], ids, ch.CommitModeLocal)

	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkAppendMessageDBStoreAdapterAsync(b *testing.B) {
	factory := store.NewMessageDBFactory(b.TempDir())
	b.Cleanup(func() { _ = factory.Close() })
	obs := &benchmarkObserver{}
	cluster, err := service.New(service.Config{
		LocalNode:             1,
		Store:                 factory,
		ReactorCount:          4,
		MailboxSize:           4096,
		AppendBatchMaxRecords: 64,
		AppendBatchMaxWait:    100 * time.Microsecond,
		Observer:              obs,
	})
	if err != nil {
		b.Skipf("message DB adapter unavailable: %v", err)
	}
	defer cluster.Close()
	ids := precreateBenchMeta(b, cluster, 128, benchMeta)
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()

	runParallelAppends(b, cluster, ids, ch.CommitModeLocal)

	b.StopTimer()
	reportBenchmarkObserver(b, obs, startGoroutines)
}

func BenchmarkAppendSingleNodeManyChannels(b *testing.B) {
	cluster := newBenchSingleNode(b)
	ctx := context.Background()
	for i := 0; i < 1024; i++ {
		meta := benchMeta(fmt.Sprintf("c-%d", i))
		if err := cluster.ApplyMeta(meta); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ch.ChannelID{ID: fmt.Sprintf("c-%d", i%1024), Type: 1}
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: uint64(i + 1), Payload: benchPayload}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppendSingleNodeHotChannel(b *testing.B) {
	cluster := newBenchSingleNode(b)
	ctx := context.Background()
	meta := benchMeta("hot")
	if err := cluster.ApplyMeta(meta); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: uint64(i + 1), Payload: benchPayload}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppendThreeNodeManyChannelsMemoryTransport(b *testing.B) {
	h := testkit.NewClusterHarness(b, []ch.NodeID{1, 2, 3})
	defer h.Close()
	ctx := context.Background()
	for i := 0; i < 128; i++ {
		h.ApplyMetaToAll(benchMetaThreeNode(fmt.Sprintf("c-%d", i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ch.ChannelID{ID: fmt.Sprintf("c-%d", i%128), Type: 1}
		if _, err := h.Nodes[1].Append(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: uint64(i + 1), Payload: benchPayload}}); err != nil {
			b.Fatal(err)
		}
	}
}

func newBenchSingleNode(b *testing.B) ch.Cluster {
	b.Helper()
	cluster, err := service.New(service.Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 4})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cluster.Close() })
	return cluster
}

func newBenchSingleNodeWithObserver(b *testing.B, obs *benchmarkObserver) ch.Cluster {
	b.Helper()
	cluster, err := service.New(service.Config{
		LocalNode:             1,
		Store:                 store.NewMemoryFactory(),
		ReactorCount:          4,
		MailboxSize:           4096,
		AppendBatchMaxRecords: 64,
		AppendBatchMaxWait:    100 * time.Microsecond,
		Observer:              obs,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cluster.Close() })
	return cluster
}

func precreateBenchMeta(b *testing.B, cluster ch.Cluster, count int, makeMeta func(string) ch.Meta) []ch.ChannelID {
	b.Helper()
	ids := make([]ch.ChannelID, count)
	for i := 0; i < count; i++ {
		meta := makeMeta(fmt.Sprintf("c-%d", i))
		if err := cluster.ApplyMeta(meta); err != nil {
			b.Fatal(err)
		}
		ids[i] = meta.ID
	}
	return ids
}

func runParallelAppends(b *testing.B, cluster ch.Cluster, ids []ch.ChannelID, mode ch.CommitMode) {
	b.Helper()
	ctx := context.Background()
	var seq atomic.Uint64
	var failed atomic.Bool
	var errOnce sync.Once
	var firstErr error
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if failed.Load() {
				return
			}
			n := seq.Add(1)
			id := ids[int(n-1)%len(ids)]
			_, err := cluster.Append(ctx, ch.AppendRequest{
				ChannelID:  id,
				CommitMode: mode,
				Message: ch.Message{
					MessageID:   n,
					ChannelID:   id.ID,
					ChannelType: id.Type,
					Payload:     benchPayload,
				},
			})
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					failed.Store(true)
				})
				return
			}
		}
	})
	if firstErr != nil {
		b.Fatal(firstErr)
	}
}

type benchmarkObserver struct {
	appendBatches      atomic.Uint64
	appendBatchRecords atomic.Uint64
	appendBatchBytes   atomic.Uint64
	appendLatencyCount atomic.Uint64
	appendLatencyNanos atomic.Uint64
	workerResults      atomic.Uint64
	workerErrors       atomic.Uint64
	workerQueueSamples atomic.Uint64
	maxWorkerQueue     atomic.Uint64
	mailboxSamples     atomic.Uint64
	maxMailboxDepth    atomic.Uint64
}

func (o *benchmarkObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	o.mailboxSamples.Add(1)
	updateMaxUint64(&o.maxMailboxDepth, uint64(depth))
}

func (o *benchmarkObserver) SetWorkerQueueDepth(pool string, depth int) {
	o.workerQueueSamples.Add(1)
	updateMaxUint64(&o.maxWorkerQueue, uint64(depth))
}

func (o *benchmarkObserver) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	o.appendBatches.Add(1)
	o.appendBatchRecords.Add(uint64(records))
	o.appendBatchBytes.Add(uint64(bytes))
}

func (o *benchmarkObserver) ObserveAppendLatency(mode ch.CommitMode, d time.Duration) {
	o.appendLatencyCount.Add(1)
	o.appendLatencyNanos.Add(uint64(d.Nanoseconds()))
}

func (o *benchmarkObserver) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
	o.workerResults.Add(1)
	if err != nil {
		o.workerErrors.Add(1)
	}
}

func reportBenchmarkObserver(b *testing.B, obs *benchmarkObserver, startGoroutines int) {
	b.Helper()
	batches := obs.appendBatches.Load()
	if batches > 0 {
		b.ReportMetric(float64(obs.appendBatchRecords.Load())/float64(batches), "records/batch")
		b.ReportMetric(float64(obs.appendBatchBytes.Load())/float64(batches), "bytes/batch")
	}
	if count := obs.appendLatencyCount.Load(); count > 0 {
		b.ReportMetric(float64(obs.appendLatencyNanos.Load())/float64(count)/1000, "append-latency-us")
	}
	if b.N > 0 {
		b.ReportMetric(float64(obs.workerResults.Load())/float64(b.N), "worker-results/op")
		b.ReportMetric(float64(obs.workerQueueSamples.Load())/float64(b.N), "worker-queue-samples/op")
	}
	b.ReportMetric(float64(obs.maxWorkerQueue.Load()), "max-worker-queue")
	b.ReportMetric(float64(obs.maxMailboxDepth.Load()), "max-mailbox-depth")
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func updateMaxUint64(value *atomic.Uint64, candidate uint64) {
	for {
		current := value.Load()
		if candidate <= current || value.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func benchMeta(id string) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	return ch.Meta{Key: ch.ChannelKeyForID(channelID), ID: channelID, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
}

func benchMetaThreeNode(id string) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	return ch.Meta{Key: ch.ChannelKeyForID(channelID), ID: channelID, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
}
