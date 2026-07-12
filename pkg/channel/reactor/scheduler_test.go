package reactor

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/stretchr/testify/require"
)

func TestDueSchedulerPopsDueItemsInOrder(t *testing.T) {
	var s dueScheduler
	now := time.Unix(10, 0)
	s.push(dueItem{key: ch.ChannelKey("b"), kind: dueReplication, due: now.Add(2 * time.Second), version: 1})
	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueAppendFlush, due: now.Add(time.Second), version: 1})

	item, ok := s.popDue(now.Add(time.Second))
	require.True(t, ok)
	require.Equal(t, ch.ChannelKey("a"), item.key)
	_, ok = s.popDue(now.Add(time.Second))
	require.False(t, ok)
}

func TestDueSchedulerNextWait(t *testing.T) {
	var s dueScheduler
	now := time.Unix(10, 0)
	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueAppendFlush, due: now.Add(2 * time.Second), version: 1})
	require.Equal(t, 2*time.Second, s.nextWait(now))
	require.Equal(t, time.Duration(0), s.nextWait(now.Add(3*time.Second)))
}

func TestReactorIdleWaitDoesNotSelfSpinWithoutDueWork(t *testing.T) {
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.Equal(t, time.Hour, r.idleWait(time.Unix(10, 0)))
}

func TestDueSchedulerLifecycleTickDoesNotScanLoadedIdleLeaders(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 64)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	now := time.Unix(100, 0)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, IdleEvictAfter: time.Second, IdleEvictCheckInterval: time.Hour})
	const loadedLeaders = 32
	var dueKey ch.ChannelKey
	for i := 0; i < loadedLeaders; i++ {
		meta := testMeta("idle-leader-"+strconv.Itoa(i), 1, 1)
		require.NoError(t, applyMetaDirect(t, r, meta))
		rc := r.channels[meta.Key]
		require.NotNil(t, rc)
		rc.lifecycle.lastAppendAt = now.Add(-2 * time.Second)
		if i == loadedLeaders/2 {
			dueKey = meta.Key
			rc.lifecycleDueVersion++
			r.due.push(dueItem{key: meta.Key, kind: dueLifecycle, due: now, version: rc.lifecycleDueVersion})
		}
	}

	r.processDue(now)
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.Equal(t, dueKey, checkpoint.Fence.ChannelKey)
	requireNoWorkerResultKind(t, sink.results, worker.TaskStoreCheckpoint)
}

func TestProcessDueHandlesEveryReadyItem(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	now := time.Unix(110, 0)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour})
	keys := make(map[ch.ChannelKey]struct{})
	for i := 0; i < 2; i++ {
		meta := testMeta("ready-due-"+strconv.Itoa(i), 1, 1)
		require.NoError(t, applyMetaDirect(t, r, meta))
		future := NewFuture()
		r.handleAppend(appendEventWithFuture(meta, uint64(i+1), "payload", future))
		requireFuturePending(t, future)
		rc := r.channels[meta.Key]
		require.NotNil(t, rc)
		require.Len(t, rc.appendQ.pending, 1)
		rc.appendQ.flushDue = now
		rc.appendFlushDueVersion++
		r.due.push(dueItem{key: meta.Key, kind: dueAppendFlush, due: now, version: rc.appendFlushDueVersion})
		keys[meta.Key] = struct{}{}
	}

	r.processDue(now)
	for i := 0; i < 2; i++ {
		result := sink.awaitResultKind(t, worker.TaskStoreAppend)
		delete(keys, result.Fence.ChannelKey)
	}
	require.Empty(t, keys)
}

func TestReactorProcessesDueAppendFlushWhileMailboxBusy(t *testing.T) {
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{
		LocalNode:             1,
		ReactorCount:          1,
		MailboxSize:           8192,
		Store:                 factory,
		AppendBatchMaxRecords: 1024,
		AppendBatchMaxWait:    20 * time.Millisecond,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("busy-mailbox-due-append", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	appendFuture, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "payload"))
	require.NoError(t, err)
	reactor := g.reactors[0]
	queuedFuture := NewFuture()
	require.NoError(t, reactor.Submit(PriorityNormal, Event{Kind: EventCheckState, Key: meta.Key, Future: queuedFuture}))
	_, err = queuedFuture.Await(context.Background())
	require.NoError(t, err)

	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			_ = reactor.mailbox.Submit(PriorityNormal, Event{Kind: EventCheckState, Key: meta.Key, Future: NewFuture()})
		}
	}()
	defer func() {
		stop.Store(true)
		<-done
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	result, err := appendFuture.Await(ctx)
	require.NoError(t, err)
	require.NoError(t, result.Err)
}

func TestDueSchedulerCoalescesSameKindAndKey(t *testing.T) {
	var s dueScheduler
	now := time.Unix(20, 0)
	key := ch.ChannelKey("a")
	s.push(dueItem{key: key, kind: dueReplication, due: now.Add(2 * time.Second), version: 1})
	s.push(dueItem{key: key, kind: dueReplication, due: now.Add(time.Second), version: 2})

	require.Len(t, s.items, 1)
	item, ok := s.popDue(now.Add(time.Second))
	require.True(t, ok)
	require.Equal(t, dueReplication, item.kind)
	require.Equal(t, key, item.key)
	require.Equal(t, uint64(2), item.version)
	require.Equal(t, now.Add(time.Second), item.due)
}

func TestDueSchedulerKeepsDifferentKindOrKeyDistinct(t *testing.T) {
	var s dueScheduler
	now := time.Unix(20, 0)
	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueReplication, due: now.Add(time.Second), version: 1})
	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueLifecycle, due: now.Add(time.Second), version: 2})
	s.push(dueItem{key: ch.ChannelKey("b"), kind: dueReplication, due: now.Add(time.Second), version: 3})

	require.Len(t, s.items, 3)
}

func TestDueSchedulerDirtyFollowerWithFutureNextPullAtWaitsForBackoff(t *testing.T) {
	net := newCapturingTransport()
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, net, sink)
	defer pools.Close()

	now := time.Unix(30, 0)
	nextPullAt := now.Add(time.Hour)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 16})
	meta := followerTestMeta("dirty-follower-backoff")
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	r.due = dueScheduler{}
	rc.replication.dirty = true
	rc.replication.nextPullAt = nextPullAt
	rc.replicationDueVersion++
	r.due.push(dueItem{key: meta.Key, kind: dueReplication, due: now, version: rc.replicationDueVersion})

	done := make(chan struct{})
	go func() {
		r.processDue(now)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("processDue spun on dirty follower before nextPullAt")
	}
	require.Equal(t, 0, net.PullCalls())
	require.Len(t, r.due.items, 1)
	require.Equal(t, nextPullAt, r.due.items[0].due)
	requireNoWorkerResultKind(t, sink.results, worker.TaskRPCPull)
}

func TestDueSchedulerActiveFollowerWithoutExplicitWorkIsNotScheduled(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	now := time.Unix(40, 0)
	meta := followerTestMeta("idle-active-follower")
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	r.due = dueScheduler{}
	rc.replication = replicationState{}

	due, ok := r.nextReplicationDue(rc, now)
	require.False(t, ok)
	require.True(t, due.IsZero())
	r.scheduleReplicationFromState(rc, now)
	require.Empty(t, r.due.items)
}

func TestCommittedCheckpointCompletionReschedulesNewerFrontier(t *testing.T) {
	factory := store.NewMemoryFactory()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 16})
	meta := followerTestMeta("committed-checkpoint-reschedule")
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	r.due = dueScheduler{}
	rc.state.LEO = 2
	rc.state.HW = 2
	rc.state.CheckpointHW = 0
	rc.replication = replicationState{}
	rc.committedCheckpointOp = 7
	rc.committedCheckpointDue = time.Now().Add(time.Second)

	r.handleStoreCheckpointResult(worker.Result{
		Kind: worker.TaskStoreCheckpoint,
		Fence: ch.Fence{
			ChannelKey:  meta.Key,
			Generation:  rc.state.Generation,
			Epoch:       rc.state.Epoch,
			LeaderEpoch: rc.state.LeaderEpoch,
			OpID:        7,
		},
		StoreCheckpoint: &worker.StoreCheckpointResult{Checkpoint: ch.Checkpoint{HW: 1}},
	})

	require.Equal(t, uint64(1), rc.state.CheckpointHW)
	require.Len(t, r.due.items, 1)
	require.Equal(t, dueReplication, r.due.items[0].kind)
	require.Equal(t, rc.committedCheckpointDue, r.due.items[0].due)
}

func TestDueSchedulerReplacementUpdatesOrdering(t *testing.T) {
	var s dueScheduler
	now := time.Unix(20, 0)
	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueReplication, due: now.Add(3 * time.Second), version: 1})
	s.push(dueItem{key: ch.ChannelKey("b"), kind: dueReplication, due: now.Add(2 * time.Second), version: 1})
	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueReplication, due: now.Add(time.Second), version: 2})

	item, ok := s.popDue(now.Add(time.Second))
	require.True(t, ok)
	require.Equal(t, ch.ChannelKey("a"), item.key)
	require.Equal(t, uint64(2), item.version)

	s.push(dueItem{key: ch.ChannelKey("a"), kind: dueReplication, due: now.Add(4 * time.Second), version: 3})
	s.push(dueItem{key: ch.ChannelKey("b"), kind: dueReplication, due: now.Add(5 * time.Second), version: 2})

	item, ok = s.popDue(now.Add(4 * time.Second))
	require.True(t, ok)
	require.Equal(t, ch.ChannelKey("a"), item.key)
	require.Equal(t, uint64(3), item.version)
	_, ok = s.popDue(now.Add(4 * time.Second))
	require.False(t, ok)
}
