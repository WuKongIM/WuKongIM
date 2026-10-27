//go:build integration

package replication

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// A returned Exchange releases its exact Channel flight before callbacks run.
// An unrelated Channel must be able to use remaining target flight capacity
// while the original callback waits on its downstream owner.
func TestPeerCallbackDoesNotHoldIndependentTargetFlight(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	executor := &manualPeerExecutor{}
	link := &recordingPeerLink{}
	b, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor, OwnerContext: owner,
		ExchangeTimeout: time.Second, MaxTargetFlight: 16,
		MaxBatchItems: 1, MaxBatchBytes: 4096,
		MaxQueuedItems: 8, MaxQueuedBytes: 32768,
		MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 16384,
	})
	require.NoError(t, err)
	type callbackState struct {
		result                   ReplicateResult
		err                      error
		inflight, owned, workers int
	}
	aEntered := make(chan callbackState, 1)
	aCompleted, bCompleted := make(chan callbackState, 1), make(chan callbackState, 1)
	aRelease, aJoined := make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(aRelease) }) }
	var bJoined chan struct{}
	var aCalls, bCalls atomic.Int32
	t.Cleanup(func() {
		unblock()
		select {
		case <-aJoined:
		case <-time.After(time.Second):
			t.Error("first peer owner did not join after callback release")
		}
		if bJoined != nil {
			select {
			case <-bJoined:
			case <-time.After(time.Second):
				t.Error("independent peer owner did not join")
			}
		}
		// On the expected pre-fix failure, releasing A drains the queued B.
		// Finish any already accepted bounded owner tasks before cancellation.
		for i := 0; i < 8 && executor.Len() > 0; i++ {
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
	a := testReplicateRequest(t, "1:callback-owner-A", "callback-owner-A", 1, []byte("a"))
	require.NoError(t, b.submit(owner, 2, a, func(result ReplicateResult, err error) {
		aCalls.Add(1)
		b.mu.Lock()
		target := b.targets[2]
		state := callbackState{result: result, err: err, inflight: len(target.inflight), owned: target.ownedItems, workers: target.workerCount()}
		b.mu.Unlock()
		aEntered <- state
		<-aRelease
		aCompleted <- state
	}))
	require.Equal(t, 1, executor.Len())
	go func() { defer close(aJoined); executor.RunNext() }()
	var held callbackState
	select {
	case held = <-aEntered:
	case <-time.After(time.Second):
		t.Fatal("first callback did not enter after Exchange returned")
	}
	require.NoError(t, held.err)
	require.Equal(t, ReplicateDurable, held.result.Status)
	require.Zero(t, held.inflight, "the reproduction starts only after A's physical exchange has released its Channel")
	require.Zero(t, held.owned, "A's accepted item credit must already have been returned")
	require.Less(t, held.workers, 16, "the target must have remaining configured flight capacity")
	independent := testReplicateRequest(t, "1:callback-owner-B", "callback-owner-B", 2, []byte("b"))
	require.NotEqual(t, a.ChannelKey, independent.ChannelKey)
	require.NoError(t, b.submit(owner, ch.NodeID(2), independent, func(result ReplicateResult, err error) {
		bCalls.Add(1)
		bCompleted <- callbackState{result: result, err: err}
	}))
	require.Equal(t, 1, executor.Len(), "an independent Channel must get a remaining flight while A's callback is blocked")
	bJoined = make(chan struct{})
	go func() { defer close(bJoined); executor.RunNext() }()
	select {
	case completed := <-bCompleted:
		require.NoError(t, completed.err)
		require.Equal(t, ReplicateDurable, completed.result.Status)
	case <-time.After(time.Second):
		t.Fatal("independent Channel could not complete while A's callback was held")
	}
	select {
	case <-aCompleted:
		t.Fatal("the first callback escaped its explicit gate")
	default:
	}
	unblock()
	select {
	case completed := <-aCompleted:
		require.NoError(t, completed.err)
		require.Equal(t, ReplicateDurable, completed.result.Status)
	case <-time.After(time.Second):
		t.Fatal("first callback did not finish after release")
	}
	require.Equal(t, int32(1), aCalls.Load())
	require.Equal(t, int32(1), bCalls.Load())
}
