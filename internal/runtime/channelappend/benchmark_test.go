package channelappend

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

var benchmarkPayload = []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
var benchmarkNilGroupMetrics *groupMetrics

func BenchmarkSubmitLocalHotChannel(b *testing.B) {
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:               1,
		AuthorityShardCount:       1,
		EffectPoolSize:            4,
		AdmissionCapacityPerShard: 4096,
		InboxCoalesceWindow:       -time.Nanosecond,
	})
	target := benchmarkAuthorityTarget("bench-hot")
	item := benchmarkSendItem("bench-hot")
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := submitAndWaitBenchmark(group, target, item)
		if err != nil {
			b.Fatalf("submit/wait error = %v", err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
			b.Fatalf("results = %#v, want one successful result", results)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkSubmitLocalHotChannelWriterPressureObserver(b *testing.B) {
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:               1,
		AuthorityShardCount:       1,
		EffectPoolSize:            4,
		AdmissionCapacityPerShard: 4096,
		InboxCoalesceWindow:       -time.Nanosecond,
		Observer:                  &benchmarkWriterPressureObserver{},
	})
	target := benchmarkAuthorityTarget("bench-hot-pressure")
	item := benchmarkSendItem("bench-hot-pressure")
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := submitAndWaitBenchmark(group, target, item)
		if err != nil {
			b.Fatalf("submit/wait error = %v", err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
			b.Fatalf("results = %#v, want one successful result", results)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkSubmitLocalHotChannelBatch16(b *testing.B) {
	const batchSize = 16
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:               1,
		AuthorityShardCount:       1,
		EffectPoolSize:            4,
		AdmissionCapacityPerShard: 4096,
		InboxCoalesceWindow:       -time.Nanosecond,
	})
	target := benchmarkAuthorityTarget("bench-hot-batch")
	items := benchmarkSendItems("bench-hot-batch", batchSize)
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := submitAndWaitBenchmarkItems(group, target, items)
		if err != nil {
			b.Fatalf("submit/wait error = %v", err)
		}
		if len(results) != batchSize {
			b.Fatalf("results = %d, want %d", len(results), batchSize)
		}
		for _, result := range results {
			if result.Err != nil || result.Result.Reason != ReasonSuccess {
				b.Fatalf("results = %#v, want successful batch", results)
			}
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkSubmitLocalHotChannelCoalescedBurst(b *testing.B) {
	const burstSize = 16
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:               1,
		AuthorityShardCount:       1,
		EffectPoolSize:            4,
		AdmissionCapacityPerShard: 4096,
		InboxCoalesceWindow:       250 * time.Microsecond,
		InboxCoalesceMaxItems:     burstSize,
	})
	target := benchmarkAuthorityTarget("bench-hot-coalesced")
	items := benchmarkSendItems("bench-hot-coalesced", burstSize)
	startGoroutines := runtime.NumGoroutine()
	futures := make([]*Future, burstSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < burstSize; j++ {
			future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{items[j]})
			if err != nil {
				b.Fatalf("submit %d error = %v", j, err)
			}
			futures[j] = future
		}
		for j, future := range futures {
			results, err := future.Wait(context.Background())
			if err != nil {
				b.Fatalf("wait %d error = %v", j, err)
			}
			if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
				b.Fatalf("results %d = %#v, want one successful result", j, results)
			}
			futures[j] = nil
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkSubmitLocalNoPersistRealtimeScoped(b *testing.B) {
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:                1,
		AuthorityShardCount:        1,
		EffectPoolSize:             4,
		AdmissionCapacityPerShard:  4096,
		InboxCoalesceWindow:        -time.Nanosecond,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 1},
		RecipientDeliveryEnqueuer:  &benchmarkRealtimeDeliveryEnqueuer{},
	})
	item := benchmarkSendItem("bench-nopersist-realtime")
	target := benchmarkAuthorityTarget(runtimechannelid.ToCommandChannel(item.Command.ChannelID))
	item.Command.NoPersist = true
	item.Command.SyncOnce = true
	item.Command.MessageScopedUIDs = []string{"u1"}
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := submitAndWaitBenchmark(group, target, item)
		if err != nil {
			b.Fatalf("submit/wait error = %v", err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
			b.Fatalf("results = %#v, want one successful realtime result", results)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkSubmitLocalManyChannelsParallel(b *testing.B) {
	const channelCount = 1024
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:               1,
		AuthorityShardCount:       maxBenchmarkInt(4, runtime.GOMAXPROCS(0)),
		EffectPoolSize:            4,
		AdmissionCapacityPerShard: 4096,
		InboxCoalesceWindow:       -time.Nanosecond,
	})
	targets := make([]AuthorityTarget, channelCount)
	items := make([]SendBatchItem, channelCount)
	for i := 0; i < channelCount; i++ {
		channelID := "bench-many-" + strconv.Itoa(i)
		targets[i] = benchmarkAuthorityTarget(channelID)
		items[i] = benchmarkSendItem(channelID)
	}
	var seq atomic.Uint64
	var failed atomic.Bool
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := seq.Add(1)
			idx := int(n % channelCount)
			results, err := submitAndWaitBenchmark(group, targets[idx], items[idx])
			if err != nil {
				if failed.CompareAndSwap(false, true) {
					b.Errorf("submit/wait error = %v", err)
				}
				continue
			}
			if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
				if failed.CompareAndSwap(false, true) {
					b.Errorf("results = %#v, want one successful result", results)
				}
			}
		}
	})
	b.StopTimer()
	if failed.Load() {
		b.FailNow()
	}
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkSubmitLocalManyChannelsParallelWriterPressureObserver(b *testing.B) {
	const channelCount = 1024
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:               1,
		AuthorityShardCount:       maxBenchmarkInt(4, runtime.GOMAXPROCS(0)),
		EffectPoolSize:            4,
		AdmissionCapacityPerShard: 4096,
		InboxCoalesceWindow:       -time.Nanosecond,
		Observer:                  &benchmarkWriterPressureObserver{},
	})
	targets := make([]AuthorityTarget, channelCount)
	items := make([]SendBatchItem, channelCount)
	for i := 0; i < channelCount; i++ {
		channelID := "bench-many-pressure-" + strconv.Itoa(i)
		targets[i] = benchmarkAuthorityTarget(channelID)
		items[i] = benchmarkSendItem(channelID)
	}
	var seq atomic.Uint64
	var failed atomic.Bool
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := seq.Add(1)
			idx := int(n % channelCount)
			results, err := submitAndWaitBenchmark(group, targets[idx], items[idx])
			if err != nil {
				if failed.CompareAndSwap(false, true) {
					b.Errorf("submit/wait error = %v", err)
				}
				continue
			}
			if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
				if failed.CompareAndSwap(false, true) {
					b.Errorf("results = %#v, want one successful result", results)
				}
			}
		}
	})
	b.StopTimer()
	if failed.Load() {
		b.FailNow()
	}
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkRouterAllItemsContextPlainBatch(b *testing.B) {
	items := make([]SendBatchItem, 256)
	for i := range items {
		items[i] = benchmarkSendItem("bench-router-context")
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, cancel := routerAllItemsContext(items)
		if err := contextErr(ctx); err != nil {
			cancel()
			b.Fatalf("routerAllItemsContext() context error = %v", err)
		}
		cancel()
	}
}

func BenchmarkGroupMetricsTransitionDisabled(b *testing.B) {
	metrics := benchmarkNilGroupMetrics

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		metrics.addPendingAppendItems(1)
		metrics.addPendingAppendItems(-1)
		metrics.addAppendInflightItems(1)
		metrics.addAppendInflightItems(-1)
		metrics.addPostCommitBacklog(1)
		metrics.addPostCommitBacklog(-1)
		metrics.observePressure()
	}
}

func BenchmarkGroupMetricsTransitionEnabled(b *testing.B) {
	metricShard := newShard(channelStateLimits{}, 4096, time.Minute)
	pool := newWorkerPool(1)
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := pool.stop(ctx); err != nil {
			b.Fatalf("worker pool stop error = %v", err)
		}
	})
	metrics := &groupMetrics{
		observer:          &benchmarkWriterPressureObserver{},
		admissionShards:   []*shard{metricShard},
		admissionCapacity: 4096,
		appendPool:        pool,
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		metrics.addPendingAppendItems(1)
		metrics.addPendingAppendItems(-1)
		metrics.addAppendInflightItems(1)
		metrics.addAppendInflightItems(-1)
		metrics.addPostCommitBacklog(1)
		metrics.addPostCommitBacklog(-1)
		metrics.observePressure()
	}
}

// submitAndWaitBenchmark submits one item and waits, retrying transient
// per-channel admission backpressure (ErrBackpressured / ErrChannelBusy) which
// is a load-shedding signal, not a correctness failure.
func submitAndWaitBenchmark(group *Group, target AuthorityTarget, item SendBatchItem) ([]SendBatchItemResult, error) {
	return submitAndWaitBenchmarkItems(group, target, []SendBatchItem{item})
}

// submitAndWaitBenchmarkItems submits a batch and waits, retrying transient
// per-channel admission backpressure (ErrBackpressured / ErrChannelBusy) which
// is a load-shedding signal, not a correctness failure.
func submitAndWaitBenchmarkItems(group *Group, target AuthorityTarget, items []SendBatchItem) ([]SendBatchItemResult, error) {
	for {
		future, err := group.SubmitLocal(context.Background(), target, items)
		if err != nil {
			if errors.Is(err, ErrBackpressured) || errors.Is(err, ErrChannelBusy) {
				runtime.Gosched()
				continue
			}
			return nil, err
		}
		results, err := future.Wait(context.Background())
		if err != nil {
			return nil, err
		}
		if benchmarkResultsBackpressured(results) {
			runtime.Gosched()
			continue
		}
		return results, nil
	}
}

func benchmarkResultsBackpressured(results []SendBatchItemResult) bool {
	for _, result := range results {
		if errors.Is(result.Err, ErrBackpressured) || errors.Is(result.Err, ErrChannelBusy) {
			return true
		}
	}
	return false
}

func newBenchmarkChannelAppendGroup(b *testing.B, opts Options) *Group {
	b.Helper()
	if opts.LocalNodeID == 0 {
		opts.LocalNodeID = 1
	}
	if opts.MessageID == nil {
		opts.MessageID = newBenchmarkMessageIDs(1)
	}
	if opts.Appender == nil {
		opts.Appender = &benchmarkAppender{}
	}
	group := New(opts)
	if err := group.Start(context.Background()); err != nil {
		b.Fatalf("Start() error = %v", err)
	}
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			b.Fatalf("Stop() error = %v", err)
		}
	})
	return group
}

func benchmarkAuthorityTarget(channelID string) AuthorityTarget {
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: channelID, Type: 2},
		LeaderNodeID: 1,
		Epoch:        1,
		LeaderEpoch:  1,
	}
	target.ChannelKey = channelKey(target.ChannelID)
	return target
}

func benchmarkSendItem(channelID string) SendBatchItem {
	return SendBatchItem{
		Context: context.Background(),
		Command: SendCommand{
			FromUID:     "bench-u",
			ChannelID:   channelID,
			ChannelType: 2,
			ClientMsgNo: "bench-client-msg",
			Payload:     benchmarkPayload,
		},
	}
}

func benchmarkSendItems(channelID string, count int) []SendBatchItem {
	items := make([]SendBatchItem, count)
	for i := range items {
		items[i] = benchmarkSendItem(channelID)
		items[i].Command.ClientMsgNo = "bench-client-msg-" + strconv.Itoa(i)
	}
	return items
}

type benchmarkMessageIDs struct {
	next atomic.Uint64
}

func newBenchmarkMessageIDs(start uint64) *benchmarkMessageIDs {
	ids := &benchmarkMessageIDs{}
	if start > 0 {
		ids.next.Store(start - 1)
	}
	return ids
}

func (ids *benchmarkMessageIDs) Next() uint64 {
	return ids.next.Add(1)
}

type benchmarkAppender struct {
	seq atomic.Uint64
}

func (a *benchmarkAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	if err := contextErr(ctx); err != nil {
		return AppendBatchResult{}, err
	}
	out := AppendBatchResult{Items: make([]AppendBatchItemResult, len(req.Messages))}
	for i, msg := range req.Messages {
		out.Items[i] = AppendBatchItemResult{
			MessageID:  msg.MessageID,
			MessageSeq: a.seq.Add(1),
			Message: Message{
				MessageID:         msg.MessageID,
				ChannelID:         msg.ChannelID,
				ChannelType:       msg.ChannelType,
				FromUID:           msg.FromUID,
				ClientMsgNo:       msg.ClientMsgNo,
				ServerTimestampMS: msg.ServerTimestampMS,
			},
		}
	}
	return out, nil
}

type benchmarkAppendOnlyObserver struct{}

func (benchmarkAppendOnlyObserver) AppendFinished(string, error, time.Duration) {}

type benchmarkWriterPressureObserver struct {
	pressure atomic.Int64
}

func (o *benchmarkWriterPressureObserver) AppendFinished(string, error, time.Duration) {}

func (o *benchmarkWriterPressureObserver) SetChannelAppendWriterPressure(event WriterPressureObservation) {
	o.pressure.Store(int64(event.PendingAppendItems + event.AppendInflightItems + event.PostCommitBacklog))
}

// benchmarkNoopActiveAdmitter enables the post-commit path (hasPostCommitWork
// == true) so advance() - not advanceAppendOnly() - is exercised, without
// adding real delivery cost. This is the path where commitEffect duffcopy
// dominates under load.
type benchmarkNoopActiveAdmitter struct{}

func (benchmarkNoopActiveAdmitter) AdmitActiveBatch(context.Context, conversationactive.ActiveBatch) error {
	return nil
}

func BenchmarkSubmitLocalHotChannelPostCommit(b *testing.B) {
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:                1,
		AuthorityShardCount:        1,
		EffectPoolSize:             4,
		AdmissionCapacityPerShard:  4096,
		InboxCoalesceWindow:        -time.Nanosecond,
		ConversationActiveAdmitter: benchmarkNoopActiveAdmitter{},
	})
	target := benchmarkAuthorityTarget("bench-postcommit")
	item := benchmarkSendItem("bench-postcommit")
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := submitAndWaitBenchmark(group, target, item)
		if err != nil {
			b.Fatalf("submit/wait error = %v", err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
			b.Fatalf("results = %#v, want one successful result", results)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkChannelAppendPostCommitPlugin(b *testing.B) {
	event := CommittedEnvelope{MessageID: 1, MessageSeq: 1, ChannelID: "room", ChannelType: 2, Payload: []byte("payload")}
	b.Run("disabled", func(b *testing.B) {
		effect := commitEffect{events: []CommittedEnvelope{event}}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = effect.run(context.Background(), commitPorts{})
		}
	})
	b.Run("enabled_enqueue", func(b *testing.B) {
		effect := commitEffect{events: []CommittedEnvelope{event}}
		enqueuer := &benchmarkPersistAfterEnqueuer{}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = effect.run(context.Background(), commitPorts{persistAfter: enqueuer})
		}
	})
}

func BenchmarkRecipientProcessorOfflineObserver(b *testing.B) {
	for _, count := range []int{16, 1024, 10000} {
		b.Run("recipients_"+strconv.Itoa(count), func(b *testing.B) {
			batch, routes := benchmarkOfflineRecipientBatch(count)
			observer := &benchmarkOfflineRecipientObserver{}
			ports := recipientPorts{
				presence:                 benchmarkPresenceResolver{routes: routes},
				offlineRecipientObserver: observer,
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := processRecipientBatch(context.Background(), batch, ports); err != nil {
					b.Fatalf("processRecipientBatch() error = %v", err)
				}
			}
			b.StopTimer()
			if observer.count == 0 {
				b.Fatal("offline observer was not invoked")
			}
		})
	}
}

func BenchmarkRecipientDeliveryWorkerEnqueue(b *testing.B) {
	observer := &benchmarkRecipientDeliveryWorkerObserver{}
	worker := NewRecipientDeliveryWorker(RecipientDeliveryWorkerOptions{
		QueueSize: defaultRecipientDeliveryQueueSize,
		Workers:   defaultRecipientDeliveryWorkers,
		Observer:  observer,
	})
	if err := worker.Start(context.Background()); err != nil {
		b.Fatalf("Start() error = %v", err)
	}
	batch := RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 1, MessageSeq: 1, ChannelID: "bench-delivery", ChannelType: 2},
		Recipients: []Recipient{{UID: "u1"}},
	}
	target := RecipientAuthorityTarget{
		HashSlot:       1,
		SlotID:         1,
		LeaderNodeID:   1,
		RouteRevision:  1,
		AuthorityEpoch: 1,
	}
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch.Event.MessageID = uint64(i + 1)
		batch.Event.MessageSeq = uint64(i + 1)
		if err := worker.EnqueueRecipientBatch(context.Background(), target, batch); err != nil {
			b.Fatalf("EnqueueRecipientBatch() error = %v", err)
		}
	}
	b.StopTimer()
	if err := worker.Stop(context.Background()); err != nil {
		b.Fatalf("Stop() error = %v", err)
	}
	if got := observer.processed.Load(); got != uint64(b.N) {
		b.Fatalf("processed = %d, want %d", got, b.N)
	}
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func maxBenchmarkInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type benchmarkRecipientDeliveryWorkerObserver struct {
	processed atomic.Uint64
}

type benchmarkPersistAfterEnqueuer struct {
	count uint64
}

func (e *benchmarkPersistAfterEnqueuer) EnqueuePersistAfter(context.Context, CommittedEnvelope) {
	e.count++
}

type benchmarkRealtimeDeliveryEnqueuer struct {
	count atomic.Uint64
}

func (e *benchmarkRealtimeDeliveryEnqueuer) EnqueueRecipientBatch(context.Context, RecipientAuthorityTarget, RecipientBatch) error {
	e.count.Add(1)
	return nil
}

func (o *benchmarkRecipientDeliveryWorkerObserver) AppendFinished(string, error, time.Duration) {
}

func (o *benchmarkRecipientDeliveryWorkerObserver) ObserveChannelAppendRecipientDeliveryProcess(RecipientDeliveryProcessObservation) {
	o.processed.Add(1)
}

type benchmarkPresenceResolver struct {
	routes []Route
}

func (r benchmarkPresenceResolver) EndpointsByUIDs(context.Context, []string) ([]Route, error) {
	return r.routes, nil
}

type benchmarkOfflineRecipientObserver struct {
	count uint64
}

func (o *benchmarkOfflineRecipientObserver) ObserveOfflineRecipient(context.Context, OfflineRecipientEvent) {
	o.count++
}

func benchmarkOfflineRecipientBatch(count int) (RecipientBatch, []Route) {
	recipients := make([]Recipient, 0, count)
	routes := make([]Route, 0, count/2)
	for i := 0; i < count; i++ {
		uid := "user-" + strconv.Itoa(i)
		recipients = append(recipients, Recipient{UID: uid})
		if i%2 == 0 {
			routes = append(routes, Route{UID: uid, OwnerNodeID: 1, SessionID: uint64(i + 1)})
		}
	}
	return RecipientBatch{
		Event: CommittedEnvelope{
			MessageID:   1,
			MessageSeq:  1,
			ChannelID:   "bench-offline",
			ChannelType: 2,
			FromUID:     "sender",
			Payload:     benchmarkPayload,
		},
		Recipients: recipients,
	}, routes
}
