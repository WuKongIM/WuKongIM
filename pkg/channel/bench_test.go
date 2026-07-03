package channel_test

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/service"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

var benchPayload = []byte("hello-channelv2")

const channelv2HeavyBenchEnv = "WK_CHANNELV2_HEAVY_BENCH"

func TestChannelV2HeavyBenchmarkEnabledReadsEnvironment(t *testing.T) {
	t.Setenv(channelv2HeavyBenchEnv, "")
	if channelv2HeavyBenchmarkEnabled() {
		t.Fatal("heavy benchmarks should be disabled by default")
	}

	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(channelv2HeavyBenchEnv, value)
			if !channelv2HeavyBenchmarkEnabled() {
				t.Fatalf("heavy benchmarks should be enabled for %q", value)
			}
		})
	}

	t.Setenv(channelv2HeavyBenchEnv, "0")
	if channelv2HeavyBenchmarkEnabled() {
		t.Fatal("heavy benchmarks should reject non-enabled values")
	}
}

func TestBenchmarkObserverCapturesWorkerAndCommitMetrics(t *testing.T) {
	obs := &benchmarkObserver{}

	obs.ObserveWorkerBatch("store-append", worker.TaskStoreAppend, 3, nil)
	obs.ObserveWorkerBatch("store-append", worker.TaskStoreAppend, 1, ch.ErrBackpressured)
	obs.SetCommitCoordinatorQueue(7, 16)
	obs.ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent{
		Requests:        4,
		Records:         6,
		Bytes:           90,
		CollectDuration: 10 * time.Microsecond,
		BuildDuration:   20 * time.Microsecond,
		CommitDuration:  30 * time.Microsecond,
		TotalDuration:   70 * time.Microsecond,
	})
	obs.ObserveCommitCoordinatorRequest(messagedb.CommitCoordinatorRequestEvent{
		Records:  2,
		Bytes:    30,
		Duration: 40 * time.Microsecond,
		Result:   "ok",
	})

	if obs.workerBatches.Load() != 2 || obs.workerBatchItems.Load() != 4 || obs.workerBatchErrors.Load() != 1 {
		t.Fatalf("worker batch metrics = batches:%d items:%d errors:%d", obs.workerBatches.Load(), obs.workerBatchItems.Load(), obs.workerBatchErrors.Load())
	}
	if obs.commitQueueCapacity.Load() != 16 || obs.maxCommitQueue.Load() != 7 {
		t.Fatalf("commit queue metrics = capacity:%d max:%d", obs.commitQueueCapacity.Load(), obs.maxCommitQueue.Load())
	}
	if obs.commitBatches.Load() != 1 || obs.commitBatchRequests.Load() != 4 || obs.commitBatchRecords.Load() != 6 || obs.commitBatchBytes.Load() != 90 {
		t.Fatalf("commit batch metrics = batches:%d requests:%d records:%d bytes:%d", obs.commitBatches.Load(), obs.commitBatchRequests.Load(), obs.commitBatchRecords.Load(), obs.commitBatchBytes.Load())
	}
	if obs.commitRequests.Load() != 1 || obs.commitRequestRecords.Load() != 2 || obs.commitRequestNanos.Load() != uint64((40*time.Microsecond).Nanoseconds()) {
		t.Fatalf("commit request metrics = requests:%d records:%d nanos:%d", obs.commitRequests.Load(), obs.commitRequestRecords.Load(), obs.commitRequestNanos.Load())
	}
}

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

func BenchmarkAppendSingleNodeHotChannelImmediateFlush(b *testing.B) {
	cluster := newBenchSingleNodeImmediateFlush(b)
	ctx := context.Background()
	meta := benchMeta("hot-immediate")
	if err := cluster.ApplyMeta(meta); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: uint64(i + 1), Payload: benchPayload}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppendSingleNodeManyChannelsImmediateFlush(b *testing.B) {
	cluster := newBenchSingleNodeImmediateFlush(b)
	ctx := context.Background()
	ids := precreateBenchMeta(b, cluster, 1024, benchMeta)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%len(ids)]
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: uint64(i + 1), Payload: benchPayload}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppendSingleNodeHotChannelAdaptiveFlush100us(b *testing.B) {
	cluster := newBenchSingleNodeAdaptiveFlush(b, 100*time.Microsecond)
	ctx := context.Background()
	meta := benchMeta("hot-adaptive-100us")
	if err := cluster.ApplyMeta(meta); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: uint64(i + 1), Payload: benchPayload}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAppendSingleNodeManyChannelsAdaptiveFlush100us(b *testing.B) {
	cluster := newBenchSingleNodeAdaptiveFlush(b, 100*time.Microsecond)
	ctx := context.Background()
	ids := precreateBenchMeta(b, cluster, 1024, benchMeta)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := ids[i%len(ids)]
		if _, err := cluster.Append(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: uint64(i + 1), Payload: benchPayload}}); err != nil {
			b.Fatal(err)
		}
	}
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

func BenchmarkAppendThreeNodeManyChannelsQuorumAsync(b *testing.B) {
	h := testkit.NewClusterHarness(b, []ch.NodeID{1, 2, 3})
	defer h.Close()
	ids := make([]ch.ChannelID, 128)
	for i := range ids {
		meta := benchMetaThreeNode(fmt.Sprintf("quorum-async-c-%d", i))
		h.ApplyMetaToAll(meta)
		ids[i] = meta.ID
	}
	startGoroutines := runtime.NumGoroutine()
	b.ReportAllocs()
	b.ResetTimer()

	runParallelAppends(b, h.Nodes[1], ids, ch.CommitModeQuorum)

	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkThreeNodeThousandChannelsIdle(b *testing.B) {
	runThreeNodeIdleBenchmark(b, 1000, "live-1k")
}

func BenchmarkThreeNodeTenThousandChannelsIdle(b *testing.B) {
	skipUnlessChannelV2HeavyBenchmark(b)
	runThreeNodeIdleBenchmark(b, 10000, "live-10k")
}

func runThreeNodeIdleBenchmark(b *testing.B, channelCount int, namePrefix string) {
	b.Helper()
	h := testkit.NewClusterHarness(b, []ch.NodeID{1, 2, 3})
	defer h.Close()
	ids := make([]ch.ChannelID, channelCount)
	for i := 0; i < channelCount; i++ {
		meta := benchMetaThreeNode(fmt.Sprintf("%s-%05d", namePrefix, i))
		h.ApplyMetaToAll(meta)
		ids[i] = meta.ID
	}
	ctx := context.Background()
	for i, id := range ids {
		_, err := h.Nodes[1].Append(ctx, ch.AppendRequest{
			ChannelID:  id,
			CommitMode: ch.CommitModeQuorum,
			Message: ch.Message{
				MessageID:   uint64(i + 1),
				ChannelID:   id.ID,
				ChannelType: id.Type,
				Payload:     benchPayload,
			},
		})
		if err != nil {
			b.Fatalf("warm channel %d: %v", i, err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := h.Nodes[1].Tick(ctx); err != nil {
			b.Fatal(err)
		}
		if err := h.Nodes[2].Tick(ctx); err != nil {
			b.Fatal(err)
		}
		if err := h.Nodes[3].Tick(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkThreeNodeTenThousandChannelsSparseTraffic(b *testing.B) {
	skipUnlessChannelV2HeavyBenchmark(b)
	h := testkit.NewClusterHarness(b, []ch.NodeID{1, 2, 3})
	defer h.Close()
	const channelCount = 10000
	const activeCount = 500
	ids := make([]ch.ChannelID, channelCount)
	for i := 0; i < channelCount; i++ {
		meta := benchMetaThreeNode(fmt.Sprintf("sparse-10k-%05d", i))
		h.ApplyMetaToAll(meta)
		ids[i] = meta.ID
	}
	ctx := context.Background()
	for i := 0; i < activeCount; i++ {
		_, err := h.Nodes[1].Append(ctx, ch.AppendRequest{
			ChannelID:  ids[i],
			CommitMode: ch.CommitModeQuorum,
			Message:    ch.Message{MessageID: uint64(i + 1), ChannelID: ids[i].ID, ChannelType: ids[i].Type, Payload: benchPayload},
		})
		if err != nil {
			b.Fatalf("warm active channel %d: %v", i, err)
		}
	}
	var seq atomic.Uint64
	var failed atomic.Bool
	var errOnce sync.Once
	var firstErr error
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if failed.Load() {
				return
			}
			n := seq.Add(1)
			id := ids[int(n-1)%activeCount]
			_, err := h.Nodes[1].Append(ctx, ch.AppendRequest{
				ChannelID:  id,
				CommitMode: ch.CommitModeQuorum,
				Message:    ch.Message{MessageID: 1000000 + n, ChannelID: id.ID, ChannelType: id.Type, Payload: benchPayload},
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

func skipUnlessChannelV2HeavyBenchmark(b *testing.B) {
	b.Helper()
	if !channelv2HeavyBenchmarkEnabled() {
		b.Skipf("set %s=1 to run 10k channelv2 benchmark scenarios", channelv2HeavyBenchEnv)
	}
}

func channelv2HeavyBenchmarkEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(channelv2HeavyBenchEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func BenchmarkAppendMessageDBStoreAdapterAsync(b *testing.B) {
	obs := &benchmarkObserver{}
	factory := newBenchmarkMessageDBFactory(b, obs, store.MessageDBFactoryOptions{})
	benchmarkAppendMessageDBStoreAdapterConfigured(b, messageDBAppendBenchConfig{
		factory:               factory,
		observer:              obs,
		appendBatchMaxRecords: 64,
		appendBatchMaxWait:    100 * time.Microsecond,
	})
}

func BenchmarkAppendMessageDBStoreAdapterDefaultWaitAsync(b *testing.B) {
	obs := &benchmarkObserver{}
	factory := newBenchmarkMessageDBFactory(b, obs, store.MessageDBFactoryOptions{})
	benchmarkAppendMessageDBStoreAdapterConfigured(b, messageDBAppendBenchConfig{
		factory:               factory,
		observer:              obs,
		appendBatchMaxRecords: 64,
	})
}

func BenchmarkAppendMessageDBStoreAdapterAdaptiveFlush100us(b *testing.B) {
	benchmarkAppendMessageDBStoreAdapterAdaptiveFlush(b, 100*time.Microsecond, 0)
}

func BenchmarkAppendMessageDBStoreAdapterAdaptiveFlushStoreWait100us(b *testing.B) {
	benchmarkAppendMessageDBStoreAdapterAdaptiveFlush(b, 100*time.Microsecond, 100*time.Microsecond)
}

func BenchmarkAppendMessageDBStoreAdapterCommitMatrix(b *testing.B) {
	cases := []struct {
		name                    string
		commitFlushWindow       time.Duration
		commitShards            int
		storeAppendBatchMaxWait time.Duration
	}{
		{name: "commit200us_storeDefault_shard1", commitFlushWindow: 200 * time.Microsecond, commitShards: 1},
		{name: "commit100us_store100us_shard1", commitFlushWindow: 100 * time.Microsecond, storeAppendBatchMaxWait: 100 * time.Microsecond, commitShards: 1},
		{name: "commit50us_store50us_shard1", commitFlushWindow: 50 * time.Microsecond, storeAppendBatchMaxWait: 50 * time.Microsecond, commitShards: 1},
		{name: "commit100us_store100us_shard4", commitFlushWindow: 100 * time.Microsecond, storeAppendBatchMaxWait: 100 * time.Microsecond, commitShards: 4},
		{name: "commit50us_store50us_shard4", commitFlushWindow: 50 * time.Microsecond, storeAppendBatchMaxWait: 50 * time.Microsecond, commitShards: 4},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			obs := &benchmarkObserver{}
			factory := newBenchmarkMessageDBFactory(b, obs, store.MessageDBFactoryOptions{
				CommitFlushWindow: tc.commitFlushWindow,
				CommitShards:      tc.commitShards,
			})
			benchmarkAppendMessageDBStoreAdapterConfigured(b, messageDBAppendBenchConfig{
				factory:                  factory,
				observer:                 obs,
				appendBatchMaxRecords:    64,
				appendBatchMaxWait:       time.Millisecond,
				appendBatchAdaptiveFlush: true,
				appendBatchColdMaxWait:   100 * time.Microsecond,
				storeAppendBatchMaxWait:  tc.storeAppendBatchMaxWait,
			})
		})
	}
}

func benchmarkAppendMessageDBStoreAdapterAdaptiveFlush(b *testing.B, coldMaxWait time.Duration, storeAppendBatchMaxWait time.Duration) {
	b.Helper()
	obs := &benchmarkObserver{}
	factory := newBenchmarkMessageDBFactory(b, obs, store.MessageDBFactoryOptions{})
	benchmarkAppendMessageDBStoreAdapterConfigured(b, messageDBAppendBenchConfig{
		factory:                  factory,
		observer:                 obs,
		appendBatchMaxRecords:    64,
		appendBatchMaxWait:       time.Millisecond,
		appendBatchAdaptiveFlush: true,
		appendBatchColdMaxWait:   coldMaxWait,
		storeAppendBatchMaxWait:  storeAppendBatchMaxWait,
	})
}

type messageDBAppendBenchConfig struct {
	factory                  *store.MessageDBFactory
	observer                 *benchmarkObserver
	appendBatchMaxRecords    int
	appendBatchMaxWait       time.Duration
	appendBatchAdaptiveFlush bool
	appendBatchColdMaxWait   time.Duration
	storeAppendBatchMaxWait  time.Duration
}

func benchmarkAppendMessageDBStoreAdapterConfigured(b *testing.B, cfg messageDBAppendBenchConfig) {
	b.Helper()
	cluster, err := service.New(service.Config{
		LocalNode:                1,
		Store:                    cfg.factory,
		ReactorCount:             4,
		MailboxSize:              4096,
		AppendBatchMaxRecords:    cfg.appendBatchMaxRecords,
		AppendBatchMaxWait:       cfg.appendBatchMaxWait,
		AppendBatchAdaptiveFlush: cfg.appendBatchAdaptiveFlush,
		AppendBatchColdMaxWait:   cfg.appendBatchColdMaxWait,
		StoreAppendBatchMaxWait:  cfg.storeAppendBatchMaxWait,
		Observer:                 cfg.observer,
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
	reportBenchmarkObserver(b, cfg.observer, startGoroutines)
}

func newBenchmarkMessageDBFactory(b *testing.B, obs *benchmarkObserver, opts store.MessageDBFactoryOptions) *store.MessageDBFactory {
	b.Helper()
	opts.CommitObserver = obs
	factory := store.NewMessageDBFactoryWithOptions(b.TempDir(), opts)
	b.Cleanup(func() { _ = factory.Close() })
	return factory
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

func newBenchSingleNodeImmediateFlush(b *testing.B) ch.Cluster {
	b.Helper()
	cluster, err := service.New(service.Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 4, AppendBatchMaxRecords: 1})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = cluster.Close() })
	return cluster
}

func newBenchSingleNodeAdaptiveFlush(b *testing.B, coldMaxWait time.Duration) ch.Cluster {
	b.Helper()
	cluster, err := service.New(service.Config{
		LocalNode:                1,
		Store:                    store.NewMemoryFactory(),
		ReactorCount:             4,
		AppendBatchAdaptiveFlush: true,
		AppendBatchColdMaxWait:   coldMaxWait,
	})
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
	appendBatches        atomic.Uint64
	appendBatchRecords   atomic.Uint64
	appendBatchBytes     atomic.Uint64
	appendLatencyCount   atomic.Uint64
	appendLatencyNanos   atomic.Uint64
	workerResults        atomic.Uint64
	workerErrors         atomic.Uint64
	workerQueueSamples   atomic.Uint64
	maxWorkerQueue       atomic.Uint64
	workerBatches        atomic.Uint64
	workerBatchItems     atomic.Uint64
	workerBatchErrors    atomic.Uint64
	maxWorkerBatchItems  atomic.Uint64
	workerWaitCount      atomic.Uint64
	workerWaitNanos      atomic.Uint64
	workerTaskCount      atomic.Uint64
	workerTaskNanos      atomic.Uint64
	mailboxSamples       atomic.Uint64
	maxMailboxDepth      atomic.Uint64
	commitQueueSamples   atomic.Uint64
	maxCommitQueue       atomic.Uint64
	commitQueueCapacity  atomic.Uint64
	commitBatches        atomic.Uint64
	commitBatchErrors    atomic.Uint64
	commitBatchRequests  atomic.Uint64
	commitBatchRecords   atomic.Uint64
	commitBatchBytes     atomic.Uint64
	commitCollectNanos   atomic.Uint64
	commitBuildNanos     atomic.Uint64
	commitDurationNanos  atomic.Uint64
	commitPublishNanos   atomic.Uint64
	commitTotalNanos     atomic.Uint64
	commitRequests       atomic.Uint64
	commitRequestErrors  atomic.Uint64
	commitRequestRecords atomic.Uint64
	commitRequestBytes   atomic.Uint64
	commitRequestNanos   atomic.Uint64
}

func (o *benchmarkObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	o.mailboxSamples.Add(1)
	updateMaxUint64(&o.maxMailboxDepth, uint64(depth))
}

func (o *benchmarkObserver) SetWorkerQueueDepth(pool string, depth int) {
	o.workerQueueSamples.Add(1)
	updateMaxUint64(&o.maxWorkerQueue, uint64(depth))
}

func (o *benchmarkObserver) ObserveWorkerWait(pool string, kind worker.TaskKind, d time.Duration) {
	o.workerWaitCount.Add(1)
	o.workerWaitNanos.Add(uint64(d.Nanoseconds()))
}

func (o *benchmarkObserver) ObserveWorkerTask(pool string, kind worker.TaskKind, err error, d time.Duration) {
	o.workerTaskCount.Add(1)
	o.workerTaskNanos.Add(uint64(d.Nanoseconds()))
}

func (o *benchmarkObserver) ObserveWorkerBatch(pool string, kind worker.TaskKind, items int, err error) {
	if items <= 0 {
		return
	}
	o.workerBatches.Add(1)
	o.workerBatchItems.Add(uint64(items))
	updateMaxUint64(&o.maxWorkerBatchItems, uint64(items))
	if err != nil {
		o.workerBatchErrors.Add(1)
	}
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

func (o *benchmarkObserver) SetCommitCoordinatorQueueDepth(depth int) {
	o.commitQueueSamples.Add(1)
	updateMaxUint64(&o.maxCommitQueue, uint64(depth))
}

func (o *benchmarkObserver) SetCommitCoordinatorQueue(depth int, capacity int) {
	o.SetCommitCoordinatorQueueDepth(depth)
	o.commitQueueCapacity.Store(uint64(capacity))
}

func (o *benchmarkObserver) ObserveCommitCoordinatorBatch(event messagedb.CommitCoordinatorBatchEvent) {
	o.commitBatches.Add(1)
	o.commitBatchRequests.Add(uint64(event.Requests))
	o.commitBatchRecords.Add(uint64(event.Records))
	o.commitBatchBytes.Add(uint64(event.Bytes))
	o.commitCollectNanos.Add(uint64(event.CollectDuration.Nanoseconds()))
	o.commitBuildNanos.Add(uint64(event.BuildDuration.Nanoseconds()))
	o.commitDurationNanos.Add(uint64(event.CommitDuration.Nanoseconds()))
	o.commitPublishNanos.Add(uint64(event.PublishDuration.Nanoseconds()))
	o.commitTotalNanos.Add(uint64(event.TotalDuration.Nanoseconds()))
	if event.Err != nil {
		o.commitBatchErrors.Add(1)
	}
}

func (o *benchmarkObserver) ObserveCommitCoordinatorRequest(event messagedb.CommitCoordinatorRequestEvent) {
	o.commitRequests.Add(1)
	o.commitRequestRecords.Add(uint64(event.Records))
	o.commitRequestBytes.Add(uint64(event.Bytes))
	o.commitRequestNanos.Add(uint64(event.Duration.Nanoseconds()))
	if event.Result != "ok" {
		o.commitRequestErrors.Add(1)
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
	if workerBatches := obs.workerBatches.Load(); workerBatches > 0 {
		b.ReportMetric(float64(workerBatches)/float64(b.N), "worker-batches/op")
		b.ReportMetric(float64(obs.workerBatchItems.Load())/float64(workerBatches), "worker-batch-items/batch")
	}
	if count := obs.workerWaitCount.Load(); count > 0 {
		b.ReportMetric(float64(obs.workerWaitNanos.Load())/float64(count)/1000, "worker-wait-us")
	}
	if count := obs.workerTaskCount.Load(); count > 0 {
		b.ReportMetric(float64(obs.workerTaskNanos.Load())/float64(count)/1000, "worker-task-us")
	}
	if commitBatches := obs.commitBatches.Load(); commitBatches > 0 {
		b.ReportMetric(float64(commitBatches)/float64(b.N), "commit-batches/op")
		b.ReportMetric(float64(obs.commitBatchRequests.Load())/float64(commitBatches), "commit-requests/batch")
		b.ReportMetric(float64(obs.commitBatchRecords.Load())/float64(commitBatches), "commit-records/batch")
		b.ReportMetric(float64(obs.commitBatchBytes.Load())/float64(commitBatches), "commit-bytes/batch")
		b.ReportMetric(float64(obs.commitCollectNanos.Load())/float64(commitBatches)/1000, "commit-collect-us")
		b.ReportMetric(float64(obs.commitBuildNanos.Load())/float64(commitBatches)/1000, "commit-build-us")
		b.ReportMetric(float64(obs.commitDurationNanos.Load())/float64(commitBatches)/1000, "commit-duration-us")
		b.ReportMetric(float64(obs.commitPublishNanos.Load())/float64(commitBatches)/1000, "commit-publish-us")
		b.ReportMetric(float64(obs.commitTotalNanos.Load())/float64(commitBatches)/1000, "commit-total-us")
	}
	if commitRequests := obs.commitRequests.Load(); commitRequests > 0 {
		b.ReportMetric(float64(commitRequests)/float64(b.N), "commit-requests/op")
		b.ReportMetric(float64(obs.commitRequestRecords.Load())/float64(commitRequests), "commit-records/request")
		b.ReportMetric(float64(obs.commitRequestNanos.Load())/float64(commitRequests)/1000, "commit-request-wait-us")
	}
	b.ReportMetric(float64(obs.maxWorkerQueue.Load()), "max-worker-queue")
	b.ReportMetric(float64(obs.maxWorkerBatchItems.Load()), "max-worker-batch-items")
	b.ReportMetric(float64(obs.maxMailboxDepth.Load()), "max-mailbox-depth")
	b.ReportMetric(float64(obs.maxCommitQueue.Load()), "max-commit-queue")
	b.ReportMetric(float64(obs.commitQueueCapacity.Load()), "commit-queue-capacity")
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
