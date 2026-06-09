package channelwrite

import (
	"context"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

var benchmarkPayload = []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

func BenchmarkSubmitLocalHotChannel(b *testing.B) {
	group := newBenchmarkChannelWriteGroup(b, Options{
		LocalNodeID:       1,
		ReactorCount:      1,
		EffectWorkerCount: 2,
		MailboxSize:       4096,
	})
	target := benchmarkAuthorityTarget("bench-hot")
	item := benchmarkSendItem("bench-hot")
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
		if err != nil {
			b.Fatalf("SubmitLocal() error = %v", err)
		}
		results, err := future.Wait(context.Background())
		if err != nil {
			b.Fatalf("Future.Wait() error = %v", err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
			b.Fatalf("results = %#v, want one successful result", results)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}

func BenchmarkSubmitLocalManyChannelsParallel(b *testing.B) {
	const channelCount = 1024
	group := newBenchmarkChannelWriteGroup(b, Options{
		LocalNodeID:       1,
		ReactorCount:      maxBenchmarkInt(4, runtime.GOMAXPROCS(0)),
		EffectWorkerCount: 2,
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
			future, err := group.SubmitLocal(context.Background(), targets[idx], []SendBatchItem{items[idx]})
			if err != nil {
				if failed.CompareAndSwap(false, true) {
					b.Errorf("SubmitLocal() error = %v", err)
				}
				continue
			}
			results, err := future.Wait(context.Background())
			if err != nil || len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
				if failed.CompareAndSwap(false, true) {
					b.Errorf("results = %#v wait_err=%v, want one successful result", results, err)
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

func BenchmarkAppendItemsWakeSignalPlainBatch(b *testing.B) {
	items := make([]preparedSend, 256)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		wake, cleanup := appendItemsWakeSignal(items)
		select {
		case <-wake:
			cleanup()
			b.Fatalf("wake signal closed for plain active items")
		default:
		}
		cleanup()
	}
}

func BenchmarkObservePressureNoPressureObserver10KChannels(b *testing.B) {
	reactor := newBenchmarkPressureReactor(10000, benchmarkAppendOnlyObserver{})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reactor.observePressure()
	}
}

func BenchmarkObservePressureWithObserver10KChannels(b *testing.B) {
	observer := &benchmarkPressureObserver{}
	reactor := newBenchmarkPressureReactor(10000, observer)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reactor.observePressure()
	}
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

func newBenchmarkPressureReactor(stateCount int, observer AppendObserver) *reactor {
	reactor := newReactor(
		0,
		1024,
		channelStateLimits{pendingItemHighWatermark: 1024, appendInflightLimit: 1},
		1,
		preparePorts{},
		appendPorts{observer: observer},
		commitPorts{},
		cursorPorts{},
	)
	for i := 0; i < stateCount; i++ {
		target := benchmarkAuthorityTarget("pressure-" + strconv.Itoa(i))
		state := newChannelState(target, reactor.limits)
		state.pendingItems = make([]preparedSend, i%3)
		state.appendInflightItems = i % 2
		state.committed = make([]CommittedEnvelope, i%5)
		reactor.states[target.ChannelKey] = state
		reactor.addPendingAppendItems(len(state.pendingItems))
		reactor.addAppendInflightItems(state.appendInflightItems)
		reactor.addPostCommitBacklog(state.commitBacklog())
	}
	return reactor
}

type benchmarkAppendOnlyObserver struct{}

func (benchmarkAppendOnlyObserver) AppendFinished(string, error, time.Duration) {}

type benchmarkPressureObserver struct {
	last ReactorPressureObservation
}

func (o *benchmarkPressureObserver) AppendFinished(string, error, time.Duration) {}

func (o *benchmarkPressureObserver) SetChannelWriteReactorPressure(event ReactorPressureObservation) {
	o.last = event
}

func maxBenchmarkInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
