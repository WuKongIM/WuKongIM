//go:build integration

package replication

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Both requests arrive before the first owner runs. B must not need a later
// admission to make progress once A releases its exchange but holds callbacks.
func TestPeerCallbackAlreadyQueuedWorkUsesRemainingTargetFlight(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	executor := &manualPeerExecutor{}
	b, err := newManualPeerBatcher(peerBatcherConfig{
		Link: &recordingPeerLink{}, Executor: executor, OwnerContext: owner,
		ExchangeTimeout: time.Second, MaxTargetFlight: 16,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 8, MaxQueuedBytes: 32768,
		MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 16384,
	})
	require.NoError(t, err)
	type completion struct {
		result ReplicateResult
		err    error
	}
	aEntered, aCompleted, bCompleted := make(chan completion, 1), make(chan completion, 1), make(chan completion, 1)
	releaseA, joinedA := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(releaseA) }) }
	var launchedA bool
	var joinedB chan struct{}
	t.Cleanup(func() {
		unblock()
		if launchedA {
			select {
			case <-joinedA:
			case <-time.After(time.Second):
				t.Error("A owner did not join after callback release")
			}
		}
		if joinedB != nil {
			select {
			case <-joinedB:
			case <-time.After(time.Second):
				t.Error("B owner did not join")
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
	a := testReplicateRequest(t, "1:prequeued-A", "prequeued-A", 1, []byte("a"))
	independent := testReplicateRequest(t, "1:prequeued-B", "prequeued-B", 2, []byte("b"))
	require.NoError(t, b.submit(owner, 2, a, func(result ReplicateResult, err error) {
		c := completion{result: result, err: err}
		aEntered <- c
		<-releaseA
		aCompleted <- c
	}))
	require.NoError(t, b.submit(owner, 2, independent, func(result ReplicateResult, err error) {
		bCompleted <- completion{result: result, err: err}
	}))
	require.Equal(t, 1, executor.Len(), "initial ready arrivals still coalesce behind one scheduled owner")
	launchedA = true
	go func() { defer close(joinedA); executor.RunNext() }()
	select {
	case c := <-aEntered:
		require.NoError(t, c.err)
		require.Equal(t, ReplicateDurable, c.result.Status)
	case <-time.After(time.Second):
		t.Fatal("A callback did not enter after its Exchange returned")
	}
	b.mu.Lock()
	inflight, owned := len(b.targets[2].inflight), b.targets[2].ownedItems
	b.mu.Unlock()
	require.Zero(t, inflight, "A's physical exchange has released its Channel")
	require.Equal(t, 2, owned, "held callback A and queued independent B both retain accepted item credit")
	// The independent I/O may now finish before this explicit callback pump
	// enters A. Either stage must retain accepted ownership for B's progress.
	require.True(t, executor.Len() > 0 || executor.CallbackLen() > 0, "prequeued B must retain an executable I/O or returned-callback owner")
	require.LessOrEqual(t, executor.Len(), 1)
	joinedB = make(chan struct{})
	go func() {
		defer close(joinedB)
		if executor.Len() > 0 {
			executor.RunNext()
		} else {
			executor.RunCallbacks()
		}
	}()
	select {
	case c := <-bCompleted:
		require.NoError(t, c.err)
		require.Equal(t, ReplicateDurable, c.result.Status)
	case <-time.After(time.Second):
		t.Fatal("prequeued B could not complete while callback A was held")
	}
	select {
	case <-aCompleted:
		t.Fatal("A callback escaped its gate")
	default:
	}
	unblock()
	select {
	case c := <-aCompleted:
		require.NoError(t, c.err)
		require.Equal(t, ReplicateDurable, c.result.Status)
	case <-time.After(time.Second):
		t.Fatal("A callback did not finish after release")
	}
}
