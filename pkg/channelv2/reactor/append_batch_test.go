package reactor

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestAppendEventsBatchByMaxRecords(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-batch-records", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 2, AppendBatchMaxWait: time.Hour})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	first, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, first)
	second, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 2, "b"))
	require.NoError(t, err)

	firstResult := awaitFutureResult(t, first)
	secondResult := awaitFutureResult(t, second)
	require.NoError(t, firstResult.Err)
	require.NoError(t, secondResult.Err)
	require.Equal(t, uint64(1), firstResult.AppendBatch.Items[0].MessageSeq)
	require.Equal(t, uint64(2), secondResult.AppendBatch.Items[0].MessageSeq)
	require.Equal(t, []int{2}, factory.appendSizes(meta.Key))
}

func TestAppendEventsBatchByMaxWaitTick(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-batch-wait", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, future)
	require.NoError(t, g.Tick(context.Background()))
	requireFuturePending(t, future)

	tickFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
	require.NoError(t, err)
	_, err = tickFuture.Await(context.Background())
	require.NoError(t, err)
	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, []int{1}, factory.appendSizes(meta.Key))
}

func TestMetadataChangeFailsInflightAppendWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	meta := testMeta("append-meta-fences-inflight", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 1})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	factory.waitAppendStarted(t)

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: updated}))
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	factory.unblockAppends()
}

func TestGroupCloseFailsInflightAppendWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	defer factory.unblockAppends()
	meta := testMeta("append-close-fails-inflight", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 1})
	requiresClose := true
	defer func() {
		if requiresClose {
			_ = g.Close()
		}
	}()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	factory.waitAppendStarted(t)

	closed := make(chan error, 1)
	go func() { closed <- g.Close() }()
	awaitCtx, awaitCancel := context.WithTimeout(context.Background(), time.Second)
	defer awaitCancel()
	_, err = future.Await(awaitCtx)
	require.ErrorIs(t, err, ch.ErrClosed)
	require.NoError(t, <-closed)
	requiresClose = false
}

func TestAppendPoolFullKeepsAcceptedRequestPendingAndRetriesOnTick(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	meta := testMeta("append-pool-full-retry", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{
		AppendBatchMaxRecords:   1,
		AppendStoreRetryBackoff: time.Hour,
		WorkerPools:             worker.PoolsConfig{StoreAppend: worker.PoolConfig{Name: "test-store-append", Workers: 1, QueueSize: 1}},
	})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	fillTask := worker.Task{
		Kind:  worker.TaskStoreAppend,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 900},
		StoreAppend: &worker.StoreAppendTask{
			ChannelID: meta.ID,
			Records:   []ch.Record{{ID: 900, Payload: []byte("fill"), SizeBytes: 4}},
			Sync:      true,
		},
	}
	require.NoError(t, g.pools.Submit(context.Background(), fillTask))
	factory.waitAppendSizes(t, meta.Key, []int{1})
	fillTask.Fence.OpID = 901
	require.NoError(t, g.pools.Submit(context.Background(), fillTask))

	third, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 3, "c"))
	require.NoError(t, err)
	requireFuturePending(t, third)

	factory.unblockAppends()
	requireFuturePending(t, third)
	tickFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
	require.NoError(t, err)
	_, err = tickFuture.Await(context.Background())
	require.NoError(t, err)
	thirdResult := awaitFutureResult(t, third)
	require.NoError(t, thirdResult.Err)
}

func TestAppendStorePoolBackpressureRollsBackBatchProposalForRetry(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	meta := testMeta("append-pool-backpressure-rollback", 1, 1)
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16,
		AppendBatchMaxRecords: 1, AppendStoreRetryBackoff: time.Millisecond,
		NextOpID: func() ch.OpID { return 100 },
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	pools, err := worker.NewPools(worker.PoolsConfig{StoreAppend: worker.PoolConfig{Name: "append", Workers: 1, QueueSize: 1}, StoreRead: worker.PoolConfig{Name: "read", Workers: 1, QueueSize: 1}, StoreApply: worker.PoolConfig{Name: "apply", Workers: 1, QueueSize: 1}, RPC: worker.PoolConfig{Name: "rpc", Workers: 1, QueueSize: 1}}, worker.Deps{LocalNode: 1, Stores: factory}, nopCompletionSink{})
	require.NoError(t, err)
	defer pools.Close()
	r.cfg.Pools = pools
	fillTask := worker.Task{
		Kind:  worker.TaskStoreAppend,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 900},
		StoreAppend: &worker.StoreAppendTask{
			ChannelID: meta.ID,
			Records:   []ch.Record{{ID: 900, Payload: []byte("fill"), SizeBytes: 4}},
			Sync:      true,
		},
	}
	require.NoError(t, pools.Submit(context.Background(), fillTask))
	factory.waitAppendStarted(t)
	fillTask.Fence.OpID = 901
	require.NoError(t, pools.Submit(context.Background(), fillTask))

	future := NewFuture()
	r.handleAppend(appendEventWithFuture(meta, 1, "a", future))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Nil(t, rc.state.InflightAppend)
	require.Empty(t, rc.state.PendingAppends)
	require.Len(t, rc.appendQ.pending, 1)
	require.Contains(t, rc.waiters, ch.OpID(1))
	requireFuturePending(t, future)
}

func TestAppendStorePoolBackpressureRollsBackMultiChannelGroupForRetry(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	metas := []ch.Meta{
		testMeta("append-pool-backpressure-group-a", 1, 1),
		testMeta("append-pool-backpressure-group-b", 1, 1),
		testMeta("append-pool-backpressure-group-c", 1, 1),
	}
	fillMeta := testMeta("append-pool-backpressure-group-fill", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{
		AppendBatchMaxRecords:   1,
		AppendStoreRetryBackoff: time.Millisecond,
		WorkerPools:             worker.PoolsConfig{StoreAppend: worker.PoolConfig{Name: "test-store-append", Workers: 1, QueueSize: 1}},
	})
	defer g.Close()
	for _, meta := range metas {
		require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	}
	fillTask := worker.Task{
		Kind:  worker.TaskStoreAppend,
		Fence: ch.Fence{ChannelKey: fillMeta.Key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 900},
		StoreAppend: &worker.StoreAppendTask{
			ChannelID: fillMeta.ID,
			Records:   []ch.Record{{ID: 900, Payload: []byte("fill"), SizeBytes: 4}},
			Sync:      true,
		},
	}
	require.NoError(t, g.pools.Submit(context.Background(), fillTask))
	factory.waitAppendSizes(t, fillMeta.Key, []int{1})
	fillTask.Fence.OpID = 901
	require.NoError(t, g.pools.Submit(context.Background(), fillTask))

	futures := make([]*Future, 0, len(metas))
	for i, meta := range metas {
		future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, uint64(i+1), string(rune('a'+i))))
		require.NoError(t, err)
		futures = append(futures, future)
	}
	for i, meta := range metas {
		tickFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
		require.NoError(t, err)
		_, err = tickFuture.Await(context.Background())
		require.NoError(t, err)
		requireFuturePending(t, futures[i])
	}

	factory.unblockAppends()
	results := waitAppendFuturesWithRetryTicks(t, g, factory, metas, futures)
	for i, meta := range metas {
		result := results[i]
		require.NoError(t, result.Err)
		require.Equal(t, uint64(1), result.AppendBatch.Items[0].MessageSeq)
		require.Equal(t, []int{1}, factory.appendSizes(meta.Key))
	}
}

func waitAppendFuturesWithRetryTicks(t *testing.T, g *Group, factory *countingStoreFactory, metas []ch.Meta, futures []*Future) []Result {
	t.Helper()
	results := make([]Result, len(futures))
	completed := make([]bool, len(futures))
	require.Eventually(t, func() bool {
		for _, meta := range metas {
			if !submitAppendRetryTick(g, meta.Key) {
				return false
			}
		}
		for i, future := range futures {
			if completed[i] {
				continue
			}
			select {
			case <-future.Done():
				results[i] = future.Result()
				completed[i] = true
			default:
			}
		}
		if !allFuturesCompleted(completed) {
			return false
		}
		for _, meta := range metas {
			if !appendSizesEqual(factory.appendSizes(meta.Key), []int{1}) {
				return false
			}
		}
		return true
	}, time.Second, time.Millisecond)
	return results
}

func submitAppendRetryTick(g *Group, key ch.ChannelKey) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	tickFuture, err := g.Submit(ctx, key, Event{Kind: EventTick, Key: key, TickNow: time.Now().Add(2 * time.Hour)})
	if err != nil {
		return false
	}
	_, err = tickFuture.Await(ctx)
	return err == nil
}

func allFuturesCompleted(completed []bool) bool {
	for _, ok := range completed {
		if !ok {
			return false
		}
	}
	return true
}

func TestAppendContextCancelRemovesAcceptedWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-cancel-removes", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	ctx, cancel := context.WithCancel(context.Background())
	future, err := g.Submit(ctx, meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, future)
	cancel()
	cleanup, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventCancelWaiter, Key: meta.Key, CancelOp: 1, CancelErr: context.Canceled})
	require.NoError(t, err)
	_, err = cleanup.Await(context.Background())
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, context.Canceled)
}

func TestAppendContextCancelDropsQueuedRequestWithoutCancelEvent(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-cancel-drops-without-event", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	ctx, cancel := context.WithCancel(context.Background())
	event := appendEvent(meta, 1, "drop-me")
	event.Context = ctx
	future, err := g.Submit(ctx, meta.Key, event)
	require.NoError(t, err)

	// A non-expired tick waits behind the append event and proves the request was admitted.
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now()}))
	requireFuturePending(t, future)
	require.Empty(t, factory.appendSizes(meta.Key))

	cancel()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)}))
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, factory.appendSizes(meta.Key))
}

func TestAppendContextCancelRemovesPostStoreQuorumWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := ch.Meta{
		Key:         ch.ChannelKey("1:append-cancel-post-store"),
		ID:          ch.ChannelID{ID: "append-cancel-post-store", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2},
		ISR:         []ch.NodeID{1, 2},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	future := NewFuture()
	require.NoError(t, rc.addWaiter(1, future))
	decision := rc.state.ProposeAppendBatch(machine.AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []machine.AppendBatchWaiter{{
			OpID:       1,
			CommitMode: ch.CommitModeQuorum,
			Records:    []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}},
		}},
	})
	require.NoError(t, decision.Err)
	require.Len(t, decision.Tasks, 1)
	task := decision.Tasks[0]
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	appendResult, err := cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: task.StoreAppend.Records, Sync: true})
	require.NoError(t, err)
	r.handleStoreAppendResult(worker.Result{
		Kind:        worker.TaskStoreAppend,
		Fence:       task.Fence,
		StoreAppend: &worker.StoreAppendResult{BaseOffset: appendResult.BaseOffset, LastOffset: appendResult.LastOffset},
	})
	require.Contains(t, rc.state.PendingAppends, ch.OpID(1))
	require.Equal(t, []ch.OpID{1}, rc.state.PendingAppendOrder)
	requireFuturePending(t, future)

	r.handleCancelWaiter(Event{Kind: EventCancelWaiter, Key: meta.Key, CancelOp: 1, CancelErr: context.Canceled, Future: NewFuture()})
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, context.Canceled)

	require.NotContains(t, rc.state.PendingAppends, ch.OpID(1))
	require.NotContains(t, rc.state.PendingAppendOrder, ch.OpID(1))
	logResult, err := cs.ReadLog(context.Background(), store.ReadLogRequest{FromOffset: 1, MaxOffset: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, logResult.Records, 1)
}

func TestAppendContextCancelSweepsPostStoreQuorumWaiterWithoutCancelEvent(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := ch.Meta{
		Key:         ch.ChannelKey("1:append-cancel-post-store-sweep"),
		ID:          ch.ChannelID{ID: "append-cancel-post-store-sweep", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2},
		ISR:         []ch.NodeID{1, 2},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	sink := captureCompletionSink{results: make(chan worker.Result, 4)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))

	ctx, cancel := context.WithCancel(context.Background())
	future := NewFuture()
	completions := 0
	future.beforeComplete = func(result Result) {
		completions++
		require.ErrorIs(t, result.Err, context.Canceled)
	}
	event := appendEventWithFuture(meta, 1, "a", future)
	event.Context = ctx
	event.Append.CommitMode = ch.CommitModeQuorum
	r.handle(event)
	r.handleStoreAppendResult(sink.awaitResult(t))

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Contains(t, rc.waiters, ch.OpID(1))
	require.Contains(t, rc.state.PendingAppends, ch.OpID(1))
	require.Equal(t, []ch.OpID{1}, rc.state.PendingAppendOrder)
	requireFuturePending(t, future)

	cancel()
	otherMeta := testMeta("append-cancel-post-store-sweep-other", 1, 1)
	otherFuture := NewFuture()
	r.handle(Event{Kind: EventApplyMeta, Key: otherMeta.Key, Meta: otherMeta, Future: otherFuture})
	_, err := otherFuture.Await(context.Background())
	require.NoError(t, err)
	err = awaitFutureError(t, future)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, completions)
	require.NotContains(t, rc.waiters, ch.OpID(1))
	require.NotContains(t, rc.state.PendingAppends, ch.OpID(1))
	require.NotContains(t, rc.state.PendingAppendOrder, ch.OpID(1))

	ackFuture := NewFuture()
	r.handle(Event{
		Kind:   EventAck,
		Key:    meta.Key,
		Ack:    transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, MatchOffset: 1},
		Future: ackFuture,
	})
	_, err = ackFuture.Await(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, completions)
}

func TestAppendContextCancelSweptByUnrelatedEventWithoutCancelEvent(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-cancel-busy-queued", 1, 1)
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16,
		AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))

	ctx, cancel := context.WithCancel(context.Background())
	future := NewFuture()
	event := appendEventWithFuture(meta, 1, "queued", future)
	event.Context = ctx
	r.handle(event)
	requireFuturePending(t, future)
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Len(t, rc.appendQ.pending, 1)
	require.Contains(t, rc.waiters, ch.OpID(1))

	cancel()
	otherMeta := testMeta("append-cancel-busy-other", 1, 1)
	otherFuture := NewFuture()
	r.handle(Event{Kind: EventApplyMeta, Key: otherMeta.Key, Meta: otherMeta, Future: otherFuture})
	_, err := otherFuture.Await(context.Background())
	require.NoError(t, err)
	err = awaitFutureError(t, future)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, rc.appendQ.pending)
	require.NotContains(t, rc.waiters, ch.OpID(1))
	require.Empty(t, rc.state.PendingAppends)
	require.Nil(t, rc.state.InflightAppend)
}

func TestAppendContextCanceledBeforeAdmissionRejectsWithoutQueueing(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-cancel-before-admission", 1, 1)
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16,
		AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	future := NewFuture()
	event := appendEventWithFuture(meta, 1, "a", future)
	event.Context = ctx

	r.handleAppend(event)

	awaitCtx, awaitCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer awaitCancel()
	_, err := future.Await(awaitCtx)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, factory.appendSizes(meta.Key))

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Empty(t, rc.appendQ.pending)
	require.Empty(t, rc.waiters)
	require.Empty(t, rc.state.PendingAppends)
	require.Nil(t, rc.state.InflightAppend)
}

func TestLeaderActivityVersionTracksDurableLEO(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("activity-version", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, nil, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))

	result := appendDirect(t, r, sink, meta, 1, "a")
	require.NoError(t, result.Err)

	rc := r.channels[meta.Key]
	require.Equal(t, rc.state.LEO, rc.lifecycle.ActivityVersion)
	require.NotZero(t, rc.lifecycle.LastAppendAt)
}

func TestAppendSendsPullHintToInactiveFollowersOncePerActivityVersion(t *testing.T) {
	factory := newCountingStoreFactory()
	tr := newTask3PullHintTransport()
	meta := testMeta("append-pull-hint", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2, 3, 4, 5}
	meta.ISR = []ch.NodeID{1}
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPoolsWithTransport(t, factory, tr, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	now := time.Now()
	rc.followers[2].Parked = true
	rc.followers[2].LastPullAt = now
	rc.followers[3].LastPullAt = now
	rc.followers[4].Stopped = true
	rc.followers[4].LastPullAt = now

	result := appendDirect(t, r, sink, meta, 1, "a")
	require.NoError(t, result.Err)

	require.Eventually(t, func() bool {
		return tr.pullHintTargets() == "2,4,5"
	}, time.Second, time.Millisecond)
	require.Equal(t, uint64(1), rc.followers[2].LastHintVersion)
	require.Equal(t, uint64(0), rc.followers[3].LastHintVersion)
	require.Equal(t, uint64(1), rc.followers[4].LastHintVersion)
	require.Equal(t, uint64(1), rc.followers[5].LastHintVersion)

	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(time.Hour), Future: NewFuture()})
	require.Equal(t, "2,4,5", tr.pullHintTargets())
}

func TestAppendSendsPullHintRetryAfterDrop(t *testing.T) {
	factory := newCountingStoreFactory()
	tr := newTask3PullHintTransport()
	tr.setDrop(2, true)
	meta := testMeta("append-pull-hint-retry", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, tr, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1, PullHintRetryInterval: time.Millisecond})
	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	rc.followers[2].Parked = true

	result := appendDirect(t, r, sink, meta, 1, "a")
	require.NoError(t, result.Err)
	r.handleRPCPullHintResult(sink.awaitResultKind(t, worker.TaskRPCPullHint))
	require.NotZero(t, rc.followers[2].HintRetryAt)
	require.False(t, rc.followers[2].HintInflight)
	droppedAttempts := tr.pullHintCount(2)
	require.Equal(t, uint64(1), rc.followers[2].LastHintVersion)
	require.Less(t, rc.followers[2].Match, rc.state.LEO)

	tr.setDrop(2, false)
	r.handleTick(Event{Kind: EventTick, Key: meta.Key, TickNow: rc.followers[2].HintRetryAt.Add(time.Millisecond), Future: NewFuture()})
	require.Eventually(t, func() bool {
		return tr.pullHintCount(2) > droppedAttempts
	}, time.Second, time.Millisecond)
}

func TestPullHintInflightAppendSendsCurrentVersionAfterOldSuccess(t *testing.T) {
	factory := newCountingStoreFactory()
	tr := newTask3PullHintTransport()
	tr.blockPullHints()
	meta := testMeta("pull-hint-inflight-success", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, tr, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	rc.followers[2].Parked = true

	first := appendDirect(t, r, sink, meta, 1, "a")
	require.NoError(t, first.Err)
	require.Eventually(t, func() bool {
		return tr.pullHintCount(2) == 1
	}, time.Second, time.Millisecond)
	require.True(t, rc.followers[2].HintInflight)
	require.Equal(t, uint64(1), tr.lastPullHintVersion(2))

	second := appendDirect(t, r, sink, meta, 2, "b")
	require.NoError(t, second.Err)
	require.Equal(t, uint64(2), rc.lifecycle.ActivityVersion)
	require.Equal(t, 1, tr.pullHintCount(2))

	tr.unblockPullHints()
	r.handleRPCPullHintResult(sink.awaitResultKind(t, worker.TaskRPCPullHint))
	require.Eventually(t, func() bool {
		return tr.hasPullHintVersion(2, 2)
	}, time.Second, time.Millisecond)
	require.Equal(t, uint64(2), rc.followers[2].LastHintVersion)
	require.Zero(t, rc.followers[2].PendingHintVersion)
}

func TestPullHintInflightAppendSchedulesCurrentVersionRetryAfterOldError(t *testing.T) {
	factory := newCountingStoreFactory()
	tr := newTask3PullHintTransport()
	tr.blockPullHints()
	meta := testMeta("pull-hint-inflight-error", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, tr, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1, PullHintRetryInterval: time.Hour})
	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	rc.followers[2].Parked = true

	first := appendDirect(t, r, sink, meta, 1, "a")
	require.NoError(t, first.Err)
	require.Eventually(t, func() bool {
		return tr.pullHintCount(2) == 1
	}, time.Second, time.Millisecond)
	require.True(t, rc.followers[2].HintInflight)

	second := appendDirect(t, r, sink, meta, 2, "b")
	require.NoError(t, second.Err)
	tr.setDrop(2, true)
	tr.unblockPullHints()

	r.handleRPCPullHintResult(sink.awaitResultKind(t, worker.TaskRPCPullHint))
	require.False(t, rc.followers[2].HintInflight)
	require.NotZero(t, rc.followers[2].HintRetryAt)
	require.Equal(t, uint64(1), rc.followers[2].LastHintVersion)
	require.Equal(t, uint64(2), rc.followers[2].PendingHintVersion)
	require.Equal(t, uint64(2), rc.lifecycle.ActivityVersion)
	require.Less(t, rc.followers[2].Match, rc.state.LEO)
}

func TestMetadataRefreshIgnoresStalePullHintResult(t *testing.T) {
	tests := []struct {
		name    string
		refresh func(ch.Meta) ch.Meta
	}{
		{
			name: "epoch",
			refresh: func(meta ch.Meta) ch.Meta {
				meta.Epoch++
				return meta
			},
		},
		{
			name: "leader-epoch",
			refresh: func(meta ch.Meta) ch.Meta {
				meta.LeaderEpoch++
				return meta
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := newCountingStoreFactory()
			tr := newTask3PullHintTransport()
			tr.blockPullHints()
			meta := testMeta("metadata-stale-pull-hint-"+tt.name, 1, 1)
			meta.Replicas = []ch.NodeID{1, 2}
			meta.ISR = []ch.NodeID{1}
			sink := captureCompletionSink{results: make(chan worker.Result, 8)}
			pools := newDirectTestPoolsWithTransport(t, factory, tr, sink)
			defer pools.Close()
			r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
			require.NoError(t, applyMetaDirect(t, r, meta))

			rc := r.channels[meta.Key]
			rc.followers[2].Parked = true
			result := appendDirect(t, r, sink, meta, 1, "a")
			require.NoError(t, result.Err)
			require.Eventually(t, func() bool {
				return tr.pullHintCount(2) == 1
			}, time.Second, time.Millisecond)
			require.True(t, rc.followers[2].HintInflight)
			require.Len(t, rc.pullHintInflight, 1)
			var staleOpID ch.OpID
			for opID := range rc.pullHintInflight {
				staleOpID = opID
			}
			require.NotZero(t, staleOpID)

			refreshed := tt.refresh(meta)
			require.NoError(t, applyMetaDirect(t, r, refreshed))
			rc = r.channels[meta.Key]
			require.Empty(t, rc.pullHintInflight)
			require.NotNil(t, rc.followers[2])
			rc.followers[2].Parked = true
			rc.followers[2].HintInflight = true
			rc.followers[2].LastHintVersion = 99
			rc.followers[2].PendingHintVersion = 99
			rc.pullHintInflight = map[ch.OpID]pullHintInflight{
				staleOpID: {follower: 2, activityVersion: 99, reason: transport.PullHintReasonAppend},
			}

			tr.unblockPullHints()
			stale := sink.awaitResultKind(t, worker.TaskRPCPullHint)
			require.Equal(t, meta.Epoch, stale.Fence.Epoch)
			require.Equal(t, meta.LeaderEpoch, stale.Fence.LeaderEpoch)
			r.handleRPCPullHintResult(stale)

			require.True(t, rc.followers[2].HintInflight)
			require.Equal(t, uint64(99), rc.followers[2].LastHintVersion)
			require.Equal(t, uint64(99), rc.followers[2].PendingHintVersion)
			require.Contains(t, rc.pullHintInflight, staleOpID)
		})
	}
}

func TestLeaderStoppedAckRequiresCurrentActivityVersionAndMatchAtLEO(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("stopped-ack-fenced", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1, 2}
	meta.MinISR = 2
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 1
	rc.state.Progress[1] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 1}
	rc.lifecycle.ActivityVersion = 3
	require.NotNil(t, rc.followers[2])
	staleFuture := NewFuture()
	r.handleAck(Event{
		Kind:   EventAck,
		Key:    meta.Key,
		Future: staleFuture,
		Ack: transport.AckRequest{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, MatchOffset: 3, ActivityVersion: 2, Stopped: true,
		},
	})
	_, err := staleFuture.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Equal(t, uint64(1), rc.state.HW)
	require.Equal(t, uint64(1), rc.state.Progress[2].Match)
	require.False(t, rc.followers[2].Stopped)
	require.Zero(t, rc.followers[2].StopAckVersion)

	laggingFuture := NewFuture()
	r.handleAck(Event{
		Kind:   EventAck,
		Key:    meta.Key,
		Future: laggingFuture,
		Ack: transport.AckRequest{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, MatchOffset: 2, ActivityVersion: 3, Stopped: true,
		},
	})
	_, err = laggingFuture.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Equal(t, uint64(1), rc.state.HW)
	require.Equal(t, uint64(1), rc.state.Progress[2].Match)
	require.False(t, rc.followers[2].Stopped)
	require.Zero(t, rc.followers[2].StopAckVersion)

	currentFuture := NewFuture()
	r.handleAck(Event{
		Kind:   EventAck,
		Key:    meta.Key,
		Future: currentFuture,
		Ack: transport.AckRequest{
			ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true,
		},
	})
	require.NoError(t, awaitFutureResult(t, currentFuture).Err)
	require.True(t, rc.followers[2].Stopped)
	require.Equal(t, uint64(3), rc.followers[2].StopAckVersion)
	require.Equal(t, uint64(3), rc.followers[2].Match)
	require.Equal(t, uint64(3), rc.state.Progress[2].Match)
}

func TestAppendAfterStopOfferSendsPullHintWithoutWaitingForParkDelay(t *testing.T) {
	factory := newCountingStoreFactory()
	tr := newTask3PullHintTransport()
	meta := testMeta("append-after-stop-offer", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPoolsWithTransport(t, factory, tr, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.followers[2].LastPullAt = time.Now()

	require.NoError(t, appendDirect(t, r, sink, meta, 1, "before stop offer").Err)
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 1}
	r.syncLeaderFollowers(rc)
	rc.followers[2].Match = 1
	rc.followers[2].LastPullAt = time.Now()
	rc.followers[2].StopOffered = true
	rc.followers[2].StopOfferedVersion = 1
	before := tr.pullHintCount(2)

	require.NoError(t, appendDirect(t, r, sink, meta, 2, "reactivate stopping follower").Err)

	require.Eventually(t, func() bool {
		return tr.pullHintCount(2) > before
	}, time.Second, time.Millisecond)
	require.Equal(t, uint64(2), tr.lastPullHintVersion(2))
	require.False(t, rc.followers[2].StopOffered)
	require.Zero(t, rc.followers[2].StopOfferedVersion)
	require.True(t, rc.followers[2].LastPullAt.IsZero())
}

func TestAppendAfterZeroVersionStopOfferSendsPullHintImmediately(t *testing.T) {
	factory := newCountingStoreFactory()
	tr := newTask3PullHintTransport()
	meta := testMeta("append-after-zero-stop-offer", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	sink := captureCompletionSink{results: make(chan worker.Result, 16)}
	pools := newDirectTestPoolsWithTransport(t, factory, tr, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 1,
		IdleEvictAfter: time.Millisecond,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.lifecycle.LastAppendAt = time.Now().Add(-time.Hour)
	rc.lifecycle.ActivityVersion = 0
	r.syncLeaderFollowers(rc)

	pullFuture := NewFuture()
	r.handlePull(Event{
		Kind:    EventPull,
		Key:     meta.Key,
		Future:  pullFuture,
		Context: context.Background(),
		Pull: transport.PullRequest{
			ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch,
			Follower: 2, NextOffset: 1, MaxBytes: 1024,
		},
		OpID: 21,
	})
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreReadLog)
	pullResult := awaitFutureResult(t, pullFuture)
	require.Equal(t, transport.PullControlStop, pullResult.Pull.Control)
	require.Zero(t, pullResult.Pull.ActivityVersion)
	require.False(t, rc.followers[2].LastPullAt.IsZero())

	result := appendDirect(t, r, sink, meta, 1, "reactivate zero stop offer")
	require.NoError(t, result.Err)

	require.Eventually(t, func() bool {
		return tr.pullHintCount(2) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, uint64(1), tr.lastPullHintVersion(2))
	require.False(t, rc.followers[2].StopOffered)
	require.True(t, rc.followers[2].LastPullAt.IsZero())
}

func appendEventWithFuture(meta ch.Meta, id uint64, payload string, future *Future) Event {
	event := appendEvent(meta, id, payload)
	event.Future = future
	return event
}

func appendDirect(t *testing.T, r *Reactor, sink captureCompletionSink, meta ch.Meta, id uint64, payload string) Result {
	t.Helper()
	future := NewFuture()
	r.handleAppend(appendEventWithFuture(meta, id, payload, future))
	r.handleStoreAppendResult(sink.awaitResultKind(t, worker.TaskStoreAppend))
	return awaitFutureResult(t, future)
}

func newAppendBatchTestGroup(t *testing.T, factory store.Factory, cfg Config) *Group {
	t.Helper()
	cfg.LocalNode = 1
	cfg.ReactorCount = 1
	cfg.MailboxSize = 16
	cfg.Store = factory
	g, err := NewGroup(cfg)
	require.NoError(t, err)
	return g
}

type countingStoreFactory struct {
	base    *store.MemoryFactory
	mu      sync.Mutex
	stores  map[ch.ChannelKey]*countingStore
	blockCh chan struct{}
	started chan struct{}
}

func newCountingStoreFactory() *countingStoreFactory {
	return &countingStoreFactory{base: store.NewMemoryFactory(), stores: make(map[ch.ChannelKey]*countingStore)}
}

func (f *countingStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing := f.stores[key]; existing != nil {
		return existing, nil
	}
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	cs := &countingStore{factory: f, key: key, base: base}
	f.stores[key] = cs
	return cs, nil
}

func (f *countingStoreFactory) appendSizes(key ch.ChannelKey) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stores[key] == nil {
		return nil
	}
	return append([]int(nil), f.stores[key].appendSizes...)
}

func (f *countingStoreFactory) waitAppendSizes(t *testing.T, key ch.ChannelKey, expected []int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return appendSizesEqual(f.appendSizes(key), expected)
	}, time.Second, time.Millisecond)
	require.Equal(t, expected, f.appendSizes(key))
}

func appendSizesEqual(actual []int, expected []int) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

type task3PullHintTransport struct {
	mu      sync.Mutex
	calls   []task3PullHintCall
	dropFor map[ch.NodeID]bool
	blockCh chan struct{}
}

type task3PullHintCall struct {
	node ch.NodeID
	req  transport.PullHintRequest
}

func newTask3PullHintTransport() *task3PullHintTransport {
	return &task3PullHintTransport{dropFor: make(map[ch.NodeID]bool)}
}

func (t *task3PullHintTransport) Pull(ctx context.Context, node ch.NodeID, req transport.PullRequest) (transport.PullResponse, error) {
	return transport.PullResponse{ChannelKey: req.ChannelKey, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, LeaderHW: req.NextOffset - 1, LeaderLEO: req.NextOffset - 1}, nil
}

func (t *task3PullHintTransport) Ack(ctx context.Context, node ch.NodeID, req transport.AckRequest) error {
	return nil
}

func (t *task3PullHintTransport) Notify(ctx context.Context, node ch.NodeID, req transport.NotifyRequest) error {
	return nil
}

func (t *task3PullHintTransport) PullHint(ctx context.Context, node ch.NodeID, req transport.PullHintRequest) error {
	t.mu.Lock()
	t.calls = append(t.calls, task3PullHintCall{node: node, req: req})
	blockCh := t.blockCh
	t.mu.Unlock()
	if blockCh != nil {
		select {
		case <-blockCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.mu.Lock()
	drop := t.dropFor[node]
	t.mu.Unlock()
	if drop {
		return context.Canceled
	}
	return nil
}

func (t *task3PullHintTransport) blockPullHints() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockCh = make(chan struct{})
}

func (t *task3PullHintTransport) unblockPullHints() {
	t.mu.Lock()
	blockCh := t.blockCh
	t.blockCh = nil
	t.mu.Unlock()
	if blockCh != nil {
		close(blockCh)
	}
}

func (t *task3PullHintTransport) setDrop(node ch.NodeID, drop bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if drop {
		t.dropFor[node] = true
		return
	}
	delete(t.dropFor, node)
}

func (t *task3PullHintTransport) pullHintTargets() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	nodes := make([]ch.NodeID, 0, len(t.calls))
	for _, call := range t.calls {
		nodes = append(nodes, call.node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
	targets := make([]byte, 0, len(nodes)*2)
	for i, node := range nodes {
		if i > 0 {
			targets = append(targets, ',')
		}
		targets = append(targets, byte('0'+node))
	}
	return string(targets)
}

func (t *task3PullHintTransport) pullHintCount(node ch.NodeID) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, call := range t.calls {
		if call.node == node {
			count++
		}
	}
	return count
}

func (t *task3PullHintTransport) lastPullHintVersion(node ch.NodeID) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	var version uint64
	for _, call := range t.calls {
		if call.node == node {
			version = call.req.ActivityVersion
		}
	}
	return version
}

func (t *task3PullHintTransport) hasPullHintVersion(node ch.NodeID, version uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, call := range t.calls {
		if call.node == node && call.req.ActivityVersion == version {
			return true
		}
	}
	return false
}

func (f *countingStoreFactory) blockAppends() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockCh = make(chan struct{})
	f.started = make(chan struct{}, 16)
}

func (f *countingStoreFactory) unblockAppends() {
	f.mu.Lock()
	blockCh := f.blockCh
	f.blockCh = nil
	f.mu.Unlock()
	if blockCh != nil {
		close(blockCh)
	}
}

func (f *countingStoreFactory) waitAppendStarted(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	started := f.started
	f.mu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for append to start")
	}
}

type countingStore struct {
	factory     *countingStoreFactory
	key         ch.ChannelKey
	base        store.ChannelStore
	appendSizes []int
}

func (s *countingStore) Load(ctx context.Context) (store.InitialState, error) {
	return s.base.Load(ctx)
}
func (s *countingStore) ApplyFollower(ctx context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	return s.base.ApplyFollower(ctx, req)
}
func (s *countingStore) ReadCommitted(ctx context.Context, req store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return s.base.ReadCommitted(ctx, req)
}
func (s *countingStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	return s.base.ReadLog(ctx, req)
}
func (s *countingStore) StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error {
	return s.base.StoreCheckpoint(ctx, checkpoint)
}
func (s *countingStore) Close() error { return s.base.Close() }

func (s *countingStore) AppendLeader(ctx context.Context, req store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	s.factory.mu.Lock()
	s.appendSizes = append(s.appendSizes, len(req.Records))
	blockCh := s.factory.blockCh
	started := s.factory.started
	s.factory.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if blockCh != nil {
		select {
		case <-blockCh:
		case <-ctx.Done():
			return store.AppendLeaderResult{}, ctx.Err()
		}
	}
	return s.base.AppendLeader(ctx, req)
}

type nopCompletionSink struct{}

func (nopCompletionSink) Complete(worker.Result) {}

type captureCompletionSink struct {
	results chan worker.Result
}

func (s captureCompletionSink) Complete(result worker.Result) {
	s.results <- result
}

func (s captureCompletionSink) awaitResult(t *testing.T) worker.Result {
	t.Helper()
	select {
	case result := <-s.results:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker result")
		return worker.Result{}
	}
}

func (s captureCompletionSink) awaitResultKind(t *testing.T, kind worker.TaskKind) worker.Result {
	t.Helper()
	deadline := time.After(time.Second)
	var deferred []worker.Result
	for {
		select {
		case result := <-s.results:
			if result.Kind == kind {
				for _, item := range deferred {
					s.results <- item
				}
				return result
			}
			deferred = append(deferred, result)
		case <-deadline:
			t.Fatalf("timed out waiting for worker result kind %v", kind)
			return worker.Result{}
		}
	}
}

func newDirectTestPools(t *testing.T, factory store.Factory, sink worker.CompletionSink) *worker.Pools {
	t.Helper()
	return newDirectTestPoolsWithTransport(t, factory, nil, sink)
}

func newDirectTestPoolsWithTransport(t *testing.T, factory store.Factory, transport transport.Client, sink worker.CompletionSink) *worker.Pools {
	t.Helper()
	pools, err := worker.NewPools(worker.PoolsConfig{
		StoreAppend: worker.PoolConfig{Name: "append", Workers: 1, QueueSize: 8},
		StoreRead:   worker.PoolConfig{Name: "read", Workers: 1, QueueSize: 8},
		StoreApply:  worker.PoolConfig{Name: "apply", Workers: 1, QueueSize: 8},
		RPC:         worker.PoolConfig{Name: "rpc", Workers: 1, QueueSize: 8},
	}, worker.Deps{LocalNode: 1, Stores: factory, Transport: transport}, sink)
	require.NoError(t, err)
	return pools
}

func awaitFutureError(t *testing.T, future *Future) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := future.Await(ctx)
	return err
}
