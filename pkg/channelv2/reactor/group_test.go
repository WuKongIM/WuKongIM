package reactor

import (
	"context"
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

func TestObserverSeesAppendBatchAndWorkerResult(t *testing.T) {
	obs := &captureObserver{}
	factory := store.NewMemoryFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory, Observer: obs, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("observer", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return obs.AppendBatches() > 0 && obs.WorkerResults() > 0
	}, time.Second, time.Millisecond)
}

func TestObserverSeesPullHintSentAndDropped(t *testing.T) {
	obs := &captureObserver{}
	factory := newCountingStoreFactory()
	tr := newTask3PullHintTransport()
	tr.setDrop(2, true)
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPoolsWithTransport(t, factory, tr, sink)
	defer pools.Close()

	meta := testMeta("observer-pull-hint", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, Observer: obs, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.followers[2].Parked = true

	result := appendDirect(t, r, sink, meta, 1, "a")
	require.NoError(t, result.Err)
	require.Equal(t, 1, obs.PullHintsSent())

	r.handleRPCPullHintResult(sink.awaitResultKind(t, worker.TaskRPCPullHint))
	require.Equal(t, 1, obs.PullHintsDropped())
}

func TestObserverSeesFollowerStopped(t *testing.T) {
	obs := &captureObserver{}
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("observer-follower-stopped", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1, 2}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, Observer: obs})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	rc.state.LEO = 3
	rc.state.HW = 3
	rc.state.Progress[1] = machine.ReplicaProgress{Match: 3}
	rc.state.Progress[2] = machine.ReplicaProgress{Match: 3}
	rc.lifecycle.ActivityVersion = 3
	r.syncLeaderFollowers(rc)

	future := NewFuture()
	r.handleAck(Event{
		Kind: EventAck, Key: meta.Key, Future: future,
		Ack: transport.AckRequest{ChannelKey: meta.Key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: 2, MatchOffset: 3, ActivityVersion: 3, Stopped: true},
	})
	require.NoError(t, awaitFutureResult(t, future).Err)
	require.Equal(t, 1, obs.FollowersStopped())
}

func TestObserverSeesChannelRuntimeEvicted(t *testing.T) {
	obs := &captureObserver{}
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()

	meta := testMeta("observer-runtime-evicted", 1, 1)
	meta.Replicas = []ch.NodeID{1}
	meta.ISR = []ch.NodeID{1}
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16, Observer: obs,
		IdleEvictAfter: time.Millisecond, IdleEvictCheckInterval: time.Millisecond, AppendBatchMaxRecords: 1,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	result := appendDirect(t, r, sink, meta, 1, "a")
	require.NoError(t, result.Err)
	rc := r.channels[meta.Key]

	r.tickLifecycle(rc, rc.lifecycle.LastAppendAt.Add(time.Hour))
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	completeLeaderCheckpointAndDue(t, r, checkpoint)

	require.Equal(t, 1, obs.RuntimeEvicted())
}

func TestGroupCompleteRoutesWorkerResultToOwningReactor(t *testing.T) {
	meta := testMeta("complete-route", 1, 1)
	g := newUnstartedTestGroup(t, 1, 8)
	fence := ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 10}

	g.Complete(worker.Result{Kind: worker.TaskFunc, Fence: fence})

	reactor := g.reactors[g.router.PickIndex(meta.Key)]
	events := reactor.mailbox.Drain(1)
	require.Len(t, events, 1)
	require.Equal(t, EventWorkerResult, events[0].Kind)
	require.Equal(t, meta.Key, events[0].Key)
	require.Equal(t, fence, events[0].Worker.Fence)
}

func TestGroupCompleteDoesNotDropWhenHighMailboxIsFull(t *testing.T) {
	meta := testMeta("completion-backpressure", 1, 1)
	g := newUnstartedTestGroup(t, 1, 1)
	reactor := g.reactors[g.router.PickIndex(meta.Key)]
	require.NoError(t, reactor.Submit(PriorityHigh, Event{Kind: EventApplyMeta, Key: ch.ChannelKey("first")}))

	done := make(chan struct{})
	fence := ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 11}
	go func() {
		g.Complete(worker.Result{Kind: worker.TaskFunc, Fence: fence})
		close(done)
	}()

	events := reactor.mailbox.Drain(1)
	require.Len(t, events, 1)
	require.Equal(t, ch.ChannelKey("first"), events[0].Key)

	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	events = reactor.mailbox.Drain(1)
	require.Len(t, events, 1)
	require.Equal(t, EventWorkerResult, events[0].Kind)
	require.Equal(t, fence, events[0].Worker.Fence)
}

func TestEventWorkerResultPriorityBeatsNormalAppendPressure(t *testing.T) {
	meta := testMeta("priority", 1, 1)
	reactor := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 1})
	require.NoError(t, reactor.Submit(eventPriority(EventAppend), appendEvent(meta, 1, "normal")))
	fence := ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, OpID: 12}
	require.NoError(t, reactor.SubmitCompletion(Event{Kind: EventWorkerResult, Key: meta.Key, Worker: worker.Result{Kind: worker.TaskFunc, Fence: fence}}))

	events := reactor.mailbox.Drain(2)
	require.Len(t, events, 2)
	require.Equal(t, EventWorkerResult, events[0].Kind)
	require.Equal(t, EventAppend, events[1].Kind)
}

func TestEventCancelWaiterPriorityBeatsNormalAppendPressure(t *testing.T) {
	meta := testMeta("cancel-priority", 1, 1)
	reactor := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 1})
	require.NoError(t, reactor.Submit(eventPriority(EventAppend), appendEvent(meta, 1, "normal")))

	err := reactor.Submit(eventPriority(EventCancelWaiter), Event{Kind: EventCancelWaiter, Key: meta.Key, CancelOp: 1})
	require.NoError(t, err)

	events := reactor.mailbox.Drain(2)
	require.Len(t, events, 2)
	require.Equal(t, EventCancelWaiter, events[0].Kind)
	require.Equal(t, EventAppend, events[1].Kind)
}

func TestDirectEventTickFutureCompletesWhenLowMailboxDrops(t *testing.T) {
	meta := testMeta("tick-drop-future", 1, 1)
	g := newUnstartedTestGroup(t, 1, 1)
	reactor := g.reactors[g.router.PickIndex(meta.Key)]
	require.NoError(t, reactor.Submit(PriorityLow, Event{Kind: EventTick, Key: meta.Key}))

	future, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventTick, Key: meta.Key})
	if err != nil {
		require.ErrorIs(t, err, ch.ErrBackpressured)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = future.Await(ctx)
	require.NoError(t, err)
}

func newUnstartedTestGroup(t *testing.T, reactorCount int, mailboxSize int) *Group {
	t.Helper()
	router, err := NewRouter(reactorCount)
	require.NoError(t, err)
	g := &Group{
		cfg:      Config{LocalNode: 1, ReactorCount: reactorCount, MailboxSize: mailboxSize, Store: store.NewMemoryFactory(), Observer: noopObserver{}},
		router:   router,
		reactors: make([]*Reactor, reactorCount),
	}
	for i := range g.reactors {
		g.reactors[i] = NewReactor(ReactorConfig{ID: i, LocalNode: 1, Store: g.cfg.Store, MailboxSize: mailboxSize})
	}
	return g
}

type captureObserver struct {
	mu            sync.Mutex
	appendBatches int
	workerResults int
	pullHintSent  int
	pullHintDrop  int
	followerStop  int
	runtimeEvict  int
}

func (o *captureObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {}

func (o *captureObserver) SetWorkerQueueDepth(pool string, depth int) {}

func (o *captureObserver) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.appendBatches++
}

func (o *captureObserver) ObserveAppendLatency(mode ch.CommitMode, d time.Duration) {}

func (o *captureObserver) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.workerResults++
}

func (o *captureObserver) ObserveChannelRuntimeLoaded(key ch.ChannelKey) {}

func (o *captureObserver) ObserveChannelRuntimeEvicted(key ch.ChannelKey, role ch.Role) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.runtimeEvict++
}

func (o *captureObserver) ObservePullHintSent(key ch.ChannelKey, follower ch.NodeID, reason transport.PullHintReason) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pullHintSent++
}

func (o *captureObserver) ObservePullHintDropped(key ch.ChannelKey, follower ch.NodeID, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pullHintDrop++
}

func (o *captureObserver) ObserveFollowerStopped(key ch.ChannelKey, follower ch.NodeID, activityVersion uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.followerStop++
}

func (o *captureObserver) AppendBatches() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.appendBatches
}

func (o *captureObserver) WorkerResults() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.workerResults
}

func (o *captureObserver) PullHintsSent() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pullHintSent
}

func (o *captureObserver) PullHintsDropped() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pullHintDrop
}

func (o *captureObserver) FollowersStopped() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.followerStop
}

func (o *captureObserver) RuntimeEvicted() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.runtimeEvict
}
