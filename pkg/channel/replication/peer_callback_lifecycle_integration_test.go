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

// A parked independent owner makes stale callback ownership observable through
// the next wave's batching behavior, without reading any phase-accounting field.
func TestPeerCallbackRetirementPreservesNextQueuedWave(t *testing.T) {
	for _, panics := range []bool{false, true} {
		name := "return"
		if panics {
			name = "panic"
		}
		t.Run(name, func(t *testing.T) {
			owner, cancel := context.WithCancel(context.Background())
			executor := &manualPeerExecutor{}
			b, err := newPeerBatcher(peerBatcherConfig{
				Link: &recordingPeerLink{}, Executor: executor, OwnerContext: owner,
				ExchangeTimeout: time.Second, MaxTargetFlight: 16,
				MaxBatchItems: 4, MaxBatchBytes: 4096,
				MaxQueuedItems: 16, MaxQueuedBytes: 32768,
				MaxTargetQueuedItems: 8, MaxTargetQueuedBytes: 16384,
			})
			require.NoError(t, err)
			entered, releaseA, joinedA := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(releaseA) }) }
			var launched bool
			t.Cleanup(func() {
				unblock()
				if launched {
					select {
					case <-joinedA:
					case <-time.After(time.Second):
						t.Error("callback owner did not join")
					}
				}
				for i := 0; i < 32 && executor.Len() > 0; i++ {
					executor.RunNext()
				}
				cancel()
				b.mu.Lock()
				defer b.mu.Unlock()
				require.Zero(t, b.ownedItems)
				require.Zero(t, b.ownedBytes)
				for _, target := range b.targets {
					require.Zero(t, target.workerCount())
					require.Empty(t, target.inflight)
				}
			})
			completed := make(chan string, 4)
			caught := make(chan any, 1)
			panicToken := &struct{}{}
			var aCalls atomic.Int32
			a := testReplicateRequest(t, "1:callback-retire-A", "callback-retire-A", 1, []byte("a"))
			require.NoError(t, b.submit(owner, 2, a, func(ReplicateResult, error) {
				aCalls.Add(1)
				close(entered)
				<-releaseA
				if panics {
					panic(panicToken)
				}
				completed <- "A"
			}))
			launched = true
			go func() {
				defer close(joinedA)
				defer func() { caught <- recover() }()
				executor.RunNext()
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("callback A did not enter")
			}
			submit := func(label string, key ch.ChannelKey, command byte) {
				t.Helper()
				req := testReplicateRequest(t, key, label, command, []byte(label))
				require.NoError(t, b.submit(owner, 2, req, func(result ReplicateResult, err error) {
					if err != nil || result.Status != ReplicateDurable {
						t.Errorf("%s completion = %+v, %v", label, result, err)
					}
					completed <- label
				}))
			}
			submit("B", "1:callback-retire-B", 2)
			require.Equal(t, 1, executor.Len(), "independent B must acquire a remaining owner while A is held")
			// Keep the newly scheduled B owner parked. On normal return, A may
			// drain B itself; on panic, its deferred retirement leaves B accepted.
			unblock()
			select {
			case <-joinedA:
			case <-time.After(time.Second):
				t.Fatal("callback A did not retire")
			}
			if panics {
				require.Same(t, panicToken, <-caught, "the callback panic must not be swallowed")
			} else {
				require.Nil(t, <-caught)
			}
			submit("C", "1:callback-retire-C", 3)
			submit("D", "1:callback-retire-D", 4)
			require.Equal(t, 1, executor.Len(), "retired callback phase must not disable initial one-owner coalescing")
			executor.RunNext()
			want := map[string]int{"B": 1, "C": 1, "D": 1}
			if !panics {
				want["A"] = 1
			}
			got := make(map[string]int)
			for range len(want) {
				select {
				case label := <-completed:
					got[label]++
				case <-time.After(time.Second):
					t.Fatal("an accepted callback did not complete")
				}
			}
			require.Equal(t, want, got)
			require.Equal(t, int32(1), aCalls.Load())
			require.Zero(t, executor.Len())
		})
	}
}

// Close must retain both callback owners, including B admitted after A's
// exchange released its Channel but before A's completion was delivered.
func TestPeerCallbackRuntimeCloseJoinsIndependentCompletionOwners(t *testing.T) {
	factory := channelstore.NewMemoryFactory()
	store, err := NewStoreAdapter(StoreAdapterConfig{Factory: factory, MaxBatchItems: MaxExchangeBatchItems, MaxBatchBytes: MaxExchangeBatchBytes})
	require.NoError(t, err)
	runtime, err := NewRuntime(RuntimeConfig{LocalNode: 1, Store: store, Link: &recordingPeerLink{}, Goroutines: goruntimeregistry.New()})
	require.NoError(t, err)
	type callbackResult struct {
		result ReplicateResult
		err    error
	}
	enteredA, enteredB := make(chan callbackResult, 1), make(chan callbackResult, 1)
	releaseA, releaseB, returnedA := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var onceA, onceB sync.Once
	unblockA := func() { onceA.Do(func() { close(releaseA) }) }
	unblockB := func() { onceB.Do(func() { close(releaseB) }) }
	var aCalls, bCalls atomic.Int32
	closeDone := make(chan struct{})
	closeStarted := false
	var closeErr error
	t.Cleanup(func() {
		unblockA()
		unblockB()
		if closeStarted {
			select {
			case <-closeDone:
				require.NoError(t, closeErr)
			case <-time.After(2 * time.Second):
				t.Error("Runtime.Close did not join its accepted callback owners")
			}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			require.NoError(t, runtime.Close(ctx))
		}
	})
	a := testReplicateRequest(t, "1:callback-close-A", "callback-close-A", 1, []byte("a"))
	require.NoError(t, runtime.peers.submit(context.Background(), 2, a, func(result ReplicateResult, err error) {
		aCalls.Add(1)
		enteredA <- callbackResult{result: result, err: err}
		<-releaseA
		close(returnedA)
	}))
	select {
	case result := <-enteredA:
		require.NoError(t, result.err)
		require.Equal(t, ReplicateDurable, result.result.Status)
	case <-time.After(time.Second):
		t.Fatal("callback A did not enter")
	}
	b := testReplicateRequest(t, "1:callback-close-B", "callback-close-B", 2, []byte("b"))
	require.NoError(t, runtime.peers.submit(context.Background(), 2, b, func(result ReplicateResult, err error) {
		bCalls.Add(1)
		enteredB <- callbackResult{result: result, err: err}
		<-releaseB
	}))
	select {
	case result := <-enteredB:
		require.NoError(t, result.err)
		require.Equal(t, ReplicateDurable, result.result.Status)
	case <-time.After(time.Second):
		t.Fatal("independent B could not enter while callback A was held")
	}
	closeStarted = true
	go func() {
		defer close(closeDone)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		closeErr = runtime.Close(ctx)
	}()
	select {
	case <-runtime.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Runtime.Close did not begin lifecycle shutdown")
	}
	select {
	case <-closeDone:
		t.Fatal("Close returned while both accepted callbacks were held")
	default:
	}
	unblockA()
	select {
	case <-returnedA:
	case <-time.After(time.Second):
		t.Fatal("callback A did not return after release")
	}
	select {
	case <-closeDone:
		t.Fatal("Close returned while callback B was still held")
	default:
	}
	unblockB()
	select {
	case <-closeDone:
		require.NoError(t, closeErr)
	case <-time.After(time.Second):
		t.Fatal("Close did not join after both callbacks returned")
	}
	require.Equal(t, int32(1), aCalls.Load())
	require.Equal(t, int32(1), bCalls.Load())
	runtime.peers.mu.Lock()
	defer runtime.peers.mu.Unlock()
	require.Zero(t, runtime.peers.ownedItems)
	require.Zero(t, runtime.peers.ownedBytes)
	for _, target := range runtime.peers.targets {
		require.Zero(t, target.workerCount())
		require.Empty(t, target.inflight)
	}
}
