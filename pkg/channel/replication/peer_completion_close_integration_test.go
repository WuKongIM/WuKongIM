//go:build integration

package replication

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

// One configured I/O worker can remain inside a later Exchange while an
// earlier returned callback executes. A shared physical pool cannot provide
// this isolation just by putting callback tasks on another internal queue.
func TestPeerRuntimeCallbackExecutionDoesNotOccupyIOWorkers(t *testing.T) {
	ioBEntered, releaseIOB := make(chan struct{}), make(chan struct{})
	callbackAEntered, releaseCallbackA := make(chan struct{}), make(chan struct{})
	var ioOnce, callbackOnce sync.Once
	unblockIO := func() { ioOnce.Do(func() { close(releaseIOB) }) }
	unblockCallback := func() { callbackOnce.Do(func() { close(releaseCallbackA) }) }
	link := readinessPeerLinkFunc(func(ctx context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
		if batch.Items[0].Replicate.ChannelKey == "1:separate-workers-B" {
			close(ioBEntered)
			select {
			case <-releaseIOB:
			case <-ctx.Done():
				return ExchangeBatchResult{}, ctx.Err()
			}
		}
		out := ExchangeBatchResult{Version: ExchangeVersion}
		for _, item := range batch.Items {
			out.Items = append(out.Items, durableExchangeResult(item))
		}
		return out, nil
	})
	store, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: MaxExchangeBatchItems, MaxBatchBytes: MaxExchangeBatchBytes})
	require.NoError(t, err)
	runtime, err := NewRuntime(RuntimeConfig{LocalNode: 1, Store: store, Link: link, Goroutines: goruntimeregistry.New(), PeerWorkers: 1, PeerTargetFlight: 1, BatchItems: 1, BatchBytes: 4096, QueueItems: 8, QueueBytes: 32768, TargetItems: 4, TargetBytes: 16384, RecoveryPageBytes: 2048})
	require.NoError(t, err)
	gate := installSharedCompletionRuntimeGate(runtime)
	t.Cleanup(func() {
		unblockIO()
		unblockCallback()
		gate.activate()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, runtime.Close(ctx))
		assertSharedCompletionRuntimeIdle(t, runtime)
	})
	a := testReplicateRequest(t, "1:separate-workers-A", "separate-workers-A", 1, []byte("a"))
	b := testReplicateRequest(t, "1:separate-workers-B", "separate-workers-B", 2, []byte("b"))
	require.NoError(t, runtime.peers.submit(context.Background(), 2, a, func(ReplicateResult, error) {
		close(callbackAEntered)
		<-releaseCallbackA
	}))
	require.NoError(t, runtime.peers.submit(context.Background(), 2, b, func(ReplicateResult, error) {}))
	gate.activate()
	select {
	case <-callbackAEntered:
	case <-time.After(time.Second):
		t.Fatal("returned callback could not execute independently from I/O")
	}
	select {
	case <-ioBEntered:
	case <-time.After(time.Second):
		t.Fatal("held callback consumed the configured I/O worker")
	}
}

// The real Runtime pool owns the gated initial task, so Close must join both
// original callbacks even when they were returned by the same Exchange.
func TestPeerSharedBatchRuntimeCloseJoinsEveryReturnedCallback(t *testing.T) {
	link := &sharedCallbackBatchLink{batches: make(chan []ch.ChannelKey, 4)}
	runtime := newSharedCompletionRuntime(t, link)
	gate := installSharedCompletionRuntimeGate(runtime)
	enteredA, enteredB := make(chan peerCompletionObservation, 1), make(chan peerCompletionObservation, 1)
	releaseA, releaseB, returnedA := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var onceA, onceB sync.Once
	unblockA := func() { onceA.Do(func() { close(releaseA) }) }
	unblockB := func() { onceB.Do(func() { close(releaseB) }) }
	var aCalls, bCalls atomic.Int32
	closed := make(chan error, 1)
	var closeStarted bool
	t.Cleanup(func() {
		unblockA()
		unblockB()
		gate.activate()
		if closeStarted {
			select {
			case err := <-closed:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Error("Runtime.Close did not join its shared-batch callback owners")
			}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			require.NoError(t, runtime.Close(ctx))
		}
		assertSharedCompletionRuntimeIdle(t, runtime)
	})
	a := testReplicateRequest(t, "1:shared-close-A", "shared-close-A", 1, []byte("a"))
	b := testReplicateRequest(t, "1:shared-close-B", "shared-close-B", 2, []byte("b"))
	require.NoError(t, runtime.peers.submit(context.Background(), 2, a, func(result ReplicateResult, err error) {
		aCalls.Add(1)
		enteredA <- peerCompletionObservation{result: result, err: err}
		<-releaseA
		close(returnedA)
	}))
	require.NoError(t, runtime.peers.submit(context.Background(), 2, b, func(result ReplicateResult, err error) {
		bCalls.Add(1)
		enteredB <- peerCompletionObservation{result: result, err: err}
		<-releaseB
	}))
	gate.activate()
	require.Equal(t, []ch.ChannelKey{a.ChannelKey, b.ChannelKey}, receivePeerBatch(t, link.batches))
	_ = receivePeerCompletion(t, enteredA)
	closeStarted = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		closed <- runtime.Close(ctx)
	}()
	select {
	case <-runtime.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Runtime.Close did not begin shutdown")
	}
	select {
	case err := <-closed:
		closeStarted = false
		t.Fatalf("Close returned with held callbacks: %v", err)
	default:
	}
	unblockA()
	select {
	case <-returnedA:
	case <-time.After(time.Second):
		t.Fatal("first callback did not return after release")
	}
	_ = receivePeerCompletion(t, enteredB)
	select {
	case err := <-closed:
		closeStarted = false
		t.Fatalf("Close returned while second callback was still held: %v", err)
	default:
	}
	unblockB()
	select {
	case err := <-closed:
		closeStarted = false
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not join after both original callbacks returned")
	}
	require.Equal(t, int32(1), aCalls.Load())
	require.Equal(t, int32(1), bCalls.Load())
}

// Refusal of callback execution does not undo an accepted result. A caller's
// Close deadline may expire, but the same cleanup owner must retry after the
// executor recovers, without another business submit or manual flush.
func TestPeerRuntimeCloseTimeoutRetainsRejectedCompletionDebt(t *testing.T) {
	var rejector *completionRejectingExecutor
	var rejectOnce sync.Once
	link := readinessPeerLinkFunc(func(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
		out := ExchangeBatchResult{Version: ExchangeVersion}
		for _, item := range batch.Items {
			out.Items = append(out.Items, durableExchangeResult(item))
		}
		rejectOnce.Do(func() { rejector.refuse.Store(true) })
		return out, nil
	})
	runtime := newSharedCompletionRuntime(t, link)
	gate := installSharedCompletionRuntimeGate(runtime)
	done := make(chan peerCompletionObservation, 2)
	var calls atomic.Int32
	t.Cleanup(func() {
		if rejector != nil {
			rejector.refuse.Store(false)
		}
		gate.activate()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("same Runtime cleanup did not complete after executor recovery: %v", err)
		}
		assertSharedCompletionRuntimeIdle(t, runtime)
	})
	runtime.peers.mu.Lock()
	callbackExecutor := runtime.peers.cfg.CallbackExecutor
	runtime.peers.mu.Unlock()
	require.NotNil(t, callbackExecutor, "the API/wiring stage must bind the real callback pool before semantic RED")
	runtime.peers.mu.Lock()
	rejector = &completionRejectingExecutor{next: callbackExecutor, rejected: make(chan struct{}, 1)}
	runtime.peers.cfg.CallbackExecutor = rejector
	runtime.peers.mu.Unlock()
	for i, label := range []string{"1:close-retry-A", "1:close-retry-B"} {
		request := testReplicateRequest(t, ch.ChannelKey(label), label, byte(i+1), []byte(label))
		require.NoError(t, runtime.peers.submit(context.Background(), 2, request, func(result ReplicateResult, err error) {
			calls.Add(1)
			done <- peerCompletionObservation{result: result, err: err}
		}))
	}
	gate.activate()
	select {
	case <-rejector.rejected:
	case <-time.After(time.Second):
		t.Fatal("returned callback debt did not reach the injected executor refusal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := runtime.Close(ctx)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, calls.Load(), "executor refusal must not fall back to inline callback execution")
	runtime.peers.mu.Lock()
	owned := runtime.peers.ownedItems
	runtime.peers.mu.Unlock()
	require.Equal(t, 2, owned, "Close timeout must retain both accepted envelopes")
	extra := testReplicateRequest(t, "1:close-retry-extra", "close-retry-extra", 3, []byte("extra"))
	require.ErrorIs(t, runtime.peers.submit(context.Background(), 2, extra, func(ReplicateResult, error) {}), ch.ErrClosed)
	rejector.refuse.Store(false)
	for i := 0; i < 2; i++ {
		result := receivePeerCompletion(t, done)
		require.NoError(t, result.err)
		require.Equal(t, ReplicateDurable, result.result.Status, "the already returned durable proof keeps its old result")
	}
	joinedCtx, joinedCancel := context.WithTimeout(context.Background(), time.Second)
	defer joinedCancel()
	require.NoError(t, runtime.Close(joinedCtx), "retry joins the same cleanup owner after the earlier caller timed out")
	require.Equal(t, int32(2), calls.Load())
}

// The initial gate wraps the existing real bounded pool. It never substitutes
// a different executor whose tasks Runtime.Close could not actually join.
type sharedCompletionRuntimeGate struct {
	next  peerExecutor
	start chan struct{}
	once  sync.Once
}

func (e *sharedCompletionRuntimeGate) Submit(task func()) error {
	return e.next.Submit(func() { <-e.start; task() })
}

func (e *sharedCompletionRuntimeGate) activate() { e.once.Do(func() { close(e.start) }) }

func installSharedCompletionRuntimeGate(runtime *Runtime) *sharedCompletionRuntimeGate {
	runtime.peers.mu.Lock()
	defer runtime.peers.mu.Unlock()
	gate := &sharedCompletionRuntimeGate{next: runtime.peers.cfg.Executor, start: make(chan struct{})}
	runtime.peers.cfg.Executor = gate
	return gate
}

func newSharedCompletionRuntime(t *testing.T, link PeerLink) *Runtime {
	t.Helper()
	store, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: MaxExchangeBatchItems, MaxBatchBytes: MaxExchangeBatchBytes})
	require.NoError(t, err)
	runtime, err := NewRuntime(RuntimeConfig{LocalNode: 1, Store: store, Link: link, Goroutines: goruntimeregistry.New(), BatchItems: 2, BatchBytes: 4096, QueueItems: 8, QueueBytes: 32768, TargetItems: 4, TargetBytes: 16384, RecoveryPageBytes: 2048, CloseTimeout: 10 * time.Millisecond})
	require.NoError(t, err)
	return runtime
}

func assertSharedCompletionRuntimeIdle(t *testing.T, runtime *Runtime) {
	t.Helper()
	runtime.peers.mu.Lock()
	defer runtime.peers.mu.Unlock()
	require.Zero(t, runtime.peers.ownedItems)
	require.Zero(t, runtime.peers.ownedBytes)
	for _, target := range runtime.peers.targets {
		require.Zero(t, target.workerCount())
		require.Empty(t, target.inflight)
	}
}
