package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
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
