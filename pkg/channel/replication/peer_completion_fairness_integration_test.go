//go:build integration

package replication

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// A Channel whose next result is already returned yields its callback worker
// to an independently ready Channel before taking another turn of its own.
func TestPeerCompletionReadyFIFOYieldsHotChannel(t *testing.T) {
	f := newSteppedPeerCompletionFixture(t, 1)
	entered, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	order := make(chan string, 3)
	a := testReplicateRequest(t, "1:callback-hot", "callback-hot", 1, []byte("a"))
	require.NoError(t, f.b.submit(f.owner, 2, a, func(ReplicateResult, error) {
		close(entered)
		<-release
		order <- "hot1"
	}))
	f.io.activate()
	waitPeerCompletionCondition(t, func() bool { return f.callbacks.len() == 1 }, "first callback was not asynchronously admitted")
	go func() { defer close(joined); f.callbacks.runOne(t) }()
	t.Cleanup(func() {
		unblock()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("held callback did not join")
		}
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first callback did not enter")
	}
	a2 := testReplicateRequest(t, a.ChannelKey, a.ChannelID.ID, 2, []byte("a2"))
	b := testReplicateRequest(t, "1:callback-cold", "callback-cold", 3, []byte("b"))
	require.NoError(t, f.b.submit(f.owner, 2, a2, func(ReplicateResult, error) { order <- "hot2" }))
	require.NoError(t, f.b.submit(f.owner, 2, b, func(ReplicateResult, error) { order <- "cold" }))
	waitPeerCompletionCondition(t, func() bool { return f.exchanges.Load() == 3 && f.ioIdle() }, "later network results did not return while the first callback was held")
	require.Zero(t, f.callbacks.len(), "one executing callback consumes the entire callback task budget")
	unblock()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("held callback did not return")
	}
	require.Equal(t, "hot1", <-order)
	waitPeerCompletionCondition(t, func() bool { return f.callbacks.len() == 1 }, "ready callbacks lost their execution owner")
	f.callbacks.runOne(t)
	require.Equal(t, "cold", <-order, "a hot Channel must yield to an already-ready independent Channel")
	waitPeerCompletionCondition(t, func() bool { return f.callbacks.len() == 1 }, "hot continuation was lost")
	f.callbacks.runOne(t)
	require.Equal(t, "hot2", <-order)
}

// Count accepted tasks before permitting any execution. This prevents a large
// underlying executor from hiding an unbounded callback scheduling queue.
func TestPeerCompletionAcceptedTaskBudget(t *testing.T) {
	for _, workers := range []int{1, 2} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			f := newSteppedPeerCompletionFixture(t, workers)
			var calls atomic.Int32
			for i := 0; i < 5; i++ {
				label := fmt.Sprintf("callback-budget-%d", i)
				r := testReplicateRequest(t, ch.ChannelKey("1:"+label), label, byte(i+1), []byte(label))
				require.NoError(t, f.b.submit(f.owner, 2, r, func(ReplicateResult, error) { calls.Add(1) }))
			}
			f.io.activate()
			waitPeerCompletionCondition(t, func() bool { return f.exchanges.Load() == 5 && f.ioIdle() }, "accepted network work did not complete")
			require.Zero(t, calls.Load(), "network workers must not execute callbacks inline")
			require.Equal(t, workers, f.callbacks.len(), "accepted callback tasks must respect the fixed callback worker budget")
			for calls.Load() < 5 {
				waitPeerCompletionCondition(t, func() bool { return f.callbacks.len() > 0 }, "accepted results lost their callback owner")
				require.LessOrEqual(t, f.callbacks.len(), workers)
				f.callbacks.runOne(t)
			}
			require.Equal(t, int32(5), calls.Load())
			require.Zero(t, f.callbacks.len())
		})
	}
}

// The executor accepts tasks without running them. Tests explicitly execute
// each accepted task; it supplies no business ordering or scheduling policy.
type steppedPeerCallbackExecutor struct {
	mu    sync.Mutex
	tasks []func()
}

func (e *steppedPeerCallbackExecutor) Submit(task func()) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.tasks) == 16 {
		return ch.ErrBackpressured
	}
	e.tasks = append(e.tasks, task)
	return nil
}

func (e *steppedPeerCallbackExecutor) len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.tasks)
}

func (e *steppedPeerCallbackExecutor) runOne(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	if len(e.tasks) == 0 {
		e.mu.Unlock()
		t.Error("no accepted callback task to execute")
		return
	}
	task := e.tasks[0]
	e.tasks[0] = nil
	e.tasks = e.tasks[1:]
	e.mu.Unlock()
	task()
}

type steppedPeerCompletionFixture struct {
	owner     context.Context
	b         *peerBatcher
	io        *sharedCallbackTestExecutor
	callbacks *steppedPeerCallbackExecutor
	exchanges atomic.Int32
}

func newSteppedPeerCompletionFixture(t *testing.T, workers int) *steppedPeerCompletionFixture {
	t.Helper()
	owner, cancel := context.WithCancel(context.Background())
	f := &steppedPeerCompletionFixture{owner: owner, io: newSharedCallbackTestExecutor(), callbacks: &steppedPeerCallbackExecutor{}}
	cfg := peerCompletionTestConfig()
	cfg.MaxBatchItems, cfg.MaxTargetQueuedItems, cfg.MaxQueuedItems = 1, 8, 16
	cfg.OwnerContext, cfg.Executor, cfg.CallbackExecutor, cfg.CallbackWorkers = owner, f.io, f.callbacks, workers
	cfg.Link = readinessPeerLinkFunc(func(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
		out := ExchangeBatchResult{Version: ExchangeVersion}
		for _, item := range batch.Items {
			out.Items = append(out.Items, durableExchangeResult(item))
			f.exchanges.Add(1)
		}
		return out, nil
	})
	var err error
	f.b, err = newPeerBatcher(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		f.io.activate()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if f.callbacks.len() > 0 {
				f.callbacks.runOne(t)
			}
			f.b.mu.Lock()
			idle := f.b.ownedItems == 0
			for _, target := range f.b.targets {
				idle = idle && target.workerCount() == 0
			}
			f.b.mu.Unlock()
			if idle {
				break
			}
			time.Sleep(time.Millisecond)
		}
		f.io.closeAndJoin(t)
		cancel()
		f.b.mu.Lock()
		defer f.b.mu.Unlock()
		require.Zero(t, f.b.ownedItems)
		require.Zero(t, f.b.ownedBytes)
		require.Empty(t, f.callbacks.tasks)
	})
	return f
}

func (f *steppedPeerCompletionFixture) ioIdle() bool {
	f.b.mu.Lock()
	defer f.b.mu.Unlock()
	for _, target := range f.b.targets {
		if len(target.inflight) > 0 || target.workerCount() > 0 {
			return false
		}
	}
	return true
}

func waitPeerCompletionCondition(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(message)
}
