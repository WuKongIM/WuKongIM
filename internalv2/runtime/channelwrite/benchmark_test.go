package channelwrite

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

var benchmarkPayload = []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
var benchmarkNilGroupMetrics *groupMetrics

func BenchmarkSubmitLocalHotChannel(b *testing.B) {
	group := newBenchmarkChannelWriteGroup(b, Options{
		LocalNodeID:       1,
		ShardCount:        1,
		AppendWorkers:     2,
		PostCommitWorkers: 2,
		MailboxSize:       4096,
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
	group := newBenchmarkChannelWriteGroup(b, Options{
		LocalNodeID:       1,
		ShardCount:        1,
		AppendWorkers:     2,
		PostCommitWorkers: 2,
		MailboxSize:       4096,
		Observer:          &benchmarkWriterPressureObserver{},
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
	group := newBenchmarkChannelWriteGroup(b, Options{
		LocalNodeID:       1,
		ShardCount:        1,
		AppendWorkers:     2,
		PostCommitWorkers: 2,
		MailboxSize:       4096,
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

func BenchmarkSubmitLocalManyChannelsParallel(b *testing.B) {
	const channelCount = 1024
	group := newBenchmarkChannelWriteGroup(b, Options{
		LocalNodeID:       1,
		ShardCount:        maxBenchmarkInt(4, runtime.GOMAXPROCS(0)),
		AppendWorkers:     2,
		PostCommitWorkers: 2,
		MailboxSize:       4096,
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
	group := newBenchmarkChannelWriteGroup(b, Options{
		LocalNodeID:       1,
		ShardCount:        maxBenchmarkInt(4, runtime.GOMAXPROCS(0)),
		AppendWorkers:     2,
		PostCommitWorkers: 2,
		MailboxSize:       4096,
		Observer:          &benchmarkWriterPressureObserver{},
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
	var admissionUsed atomic.Int64
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
		admissionUsed:     &admissionUsed,
		admissionCapacity: 4096,
		pool:              pool,
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

func newBenchmarkChannelWriteGroup(b *testing.B, opts Options) *Group {
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
		ChannelID:     ChannelID{ID: channelID, Type: 2},
		LeaderNodeID:  1,
		Epoch:         1,
		LeaderEpoch:   1,
		RouteRevision: 1,
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

func (o *benchmarkWriterPressureObserver) SetChannelWriteWriterPressure(event WriterPressureObservation) {
	o.pressure.Store(int64(event.PendingAppendItems + event.AppendInflightItems + event.PostCommitBacklog))
}

func maxBenchmarkInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
