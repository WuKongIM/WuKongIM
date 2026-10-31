//go:build integration

package replication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
	"github.com/stretchr/testify/require"
)

type gatedPhaseState struct {
	begun, ended int
	result       rpctrace.Result
}
type gatedAppendObservation struct {
	mu     sync.Mutex
	phases map[rpctrace.Stage]gatedPhaseState
}

func newGatedAppendObservation() *gatedAppendObservation {
	return &gatedAppendObservation{phases: make(map[rpctrace.Stage]gatedPhaseState)}
}
func (o *gatedAppendObservation) BeginAppendPhase(stage rpctrace.Stage) rpctrace.AppendPhaseEnd {
	o.mu.Lock()
	s := o.phases[stage]
	s.begun++
	o.phases[stage] = s
	o.mu.Unlock()
	return func(common rpctrace.Result, _ int, _ func(int) rpctrace.Result) {
		o.mu.Lock()
		s := o.phases[stage]
		s.ended++
		s.result = common
		o.phases[stage] = s
		o.mu.Unlock()
	}
}
func (o *gatedAppendObservation) state(stage rpctrace.Stage) gatedPhaseState {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.phases[stage]
}

type gatedObservationStore struct {
	ReplicaStore
	entered chan struct{}
	release chan struct{}
	failure error
}

func (s *gatedObservationStore) Sync(_ context.Context, mutations []Mutation) []MutationResult {
	close(s.entered)
	<-s.release
	out := make([]MutationResult, len(mutations))
	for i, m := range mutations {
		out[i] = MutationResult{Outcome: ch.AppendOutcomeDurable, LastOffset: m.Manifest.LastOffset}
		if i == 1 {
			out[i] = MutationResult{Outcome: ch.AppendOutcomeUnknown, Err: s.failure}
		}
	}
	return out
}

type gatedObservationPeer struct {
	entered chan struct{}
	release chan struct{}
}

func (p *gatedObservationPeer) Exchange(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	close(p.entered)
	<-p.release
	out := ExchangeBatchResult{Version: ExchangeVersion, Items: make([]ExchangeItemResult, len(batch.Items))}
	for i, item := range batch.Items {
		out.Items[i] = durableExchangeResult(item)
	}
	return out, nil
}

// Independent gates prove the observed boundaries, not a synthetic storage or
// network latency root cause. Two owners share Sync but retain their outcomes.
func TestAppendObservationLocalAndPeerGatesStayOwnerAligned(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	queueEntered, queueRelease := make(chan struct{}), make(chan struct{})
	store := &gatedObservationStore{entered: make(chan struct{}), release: make(chan struct{}), failure: errors.New("owner-B-local")}
	peer := &gatedObservationPeer{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseQueue, releaseStore, releasePeer sync.Once
	unblock := func() {
		releaseQueue.Do(func() { close(queueRelease) })
		releaseStore.Do(func() { close(store.release) })
		releasePeer.Do(func() { close(peer.release) })
	}
	t.Cleanup(unblock)
	runtime := &Runtime{ctx: owner}
	local := &runtimeLocalDurability{runtime: runtime, store: store, timeout: time.Second}
	pool, err := workqueue.NewBoundedBatchPool(workqueue.BoundedBatchPoolConfig[localDurabilityItem]{
		Name: "observed-local", Workers: 1, QueueSize: 4, ReleaseTimeout: time.Second,
		Policy: func(localDurabilityItem) workqueue.BatchOptions {
			return workqueue.BatchOptions{MaxItems: 2, MaxWait: time.Second}
		},
	}, func(ctx context.Context, items []localDurabilityItem) error {
		close(queueEntered)
		<-queueRelease
		return local.runBatch(ctx, items)
	})
	require.NoError(t, err)
	runtime.localPool = pool
	t.Cleanup(func() { unblock(); require.NoError(t, pool.Close(context.Background())) })
	observers := []*gatedAppendObservation{newGatedAppendObservation(), newGatedAppendObservation()}
	contexts := []context.Context{rpctrace.WithAppendPhaseObserver(owner, observers[0]), rpctrace.WithAppendPhaseObserver(owner, observers[1])}
	requests := []ReplicateRequest{testReplicateRequest(t, "1:observe-A", "A", 1, []byte("a")), testReplicateRequest(t, "1:observe-B", "B", 1, []byte("b"))}
	proposals := make([]durableProposal, 2)
	localDone := make(chan durabilityCompletion, 2)
	for i, q := range requests {
		proposals[i] = durableProposal{first: q.Manifest.BaseOffset + 1, last: q.Manifest.LastOffset, channelKey: q.ChannelKey, channelID: q.ChannelID, leader: q.Leader, manifest: q.Manifest, records: q.Records}
		require.NoError(t, local.submitLocal(contexts[i], proposals[i], func(c durabilityCompletion) { localDone <- c }))
	}
	select {
	case <-queueEntered:
	case <-time.After(time.Second):
		t.Fatal("local batch did not enter")
	}
	for _, o := range observers {
		s := o.state("channel_local_queue")
		require.Equal(t, 1, s.begun, "queue observation must begin at original admission")
		require.Zero(t, s.ended)
	}
	releaseQueue.Do(func() { close(queueRelease) })
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("Sync did not enter")
	}
	for _, o := range observers {
		require.Equal(t, 1, o.state("channel_local_queue").ended)
		s := o.state("channel_local_sync")
		require.Equal(t, 1, s.begun)
		require.Zero(t, s.ended)
	}
	executor := &manualPeerExecutor{}
	peers, err := newManualPeerBatcher(peerBatcherConfig{Link: peer, Executor: executor, MaxTargetFlight: 1, OwnerContext: owner, ExchangeTimeout: time.Second, MaxBatchItems: 4, MaxBatchBytes: 4096, MaxQueuedItems: 8, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 4096})
	require.NoError(t, err)
	dispatcher := &batchingDurabilityDispatcher{ownerContext: owner, peers: peers, repairs: discardFollowerRepairSink{}}
	peerDone := make(chan durabilityCompletion, 2)
	for i, p := range proposals {
		require.NoError(t, dispatcher.submitReplica(contexts[i], 2, p, func(c durabilityCompletion) { peerDone <- c }))
	}
	peerOwnerDone := make(chan struct{})
	go func() { defer close(peerOwnerDone); executor.RunNext() }()
	t.Cleanup(func() {
		unblock()
		select {
		case <-peerOwnerDone:
		case <-time.After(time.Second):
			t.Error("peer owner did not join")
		}
	})
	select {
	case <-peer.entered:
	case <-time.After(time.Second):
		t.Fatal("peer exchange did not enter")
	}
	for _, o := range observers {
		s := o.state("channel_peer_wait")
		require.Equal(t, 1, s.begun)
		require.Zero(t, s.ended)
	}
	releaseStore.Do(func() { close(store.release) })
	var localResults []durabilityCompletion
	for range 2 {
		select {
		case c := <-localDone:
			localResults = append(localResults, c)
		case <-time.After(time.Second):
			t.Fatal("local completion did not join")
		}
	}
	require.NoError(t, localResults[0].err)
	require.ErrorIs(t, localResults[1].err, store.failure)
	require.Equal(t, rpctrace.ResultOK, observers[0].state("channel_local_sync").result)
	require.Equal(t, rpctrace.ResultError, observers[1].state("channel_local_sync").result, "sibling local failure must not mark owner A")
	for _, o := range observers {
		require.Zero(t, o.state("channel_peer_wait").ended, "local Sync cannot terminate independent peer wait")
	}
	releasePeer.Do(func() { close(peer.release) })
	for range 2 {
		select {
		case c := <-peerDone:
			require.NoError(t, c.err)
		case <-time.After(time.Second):
			t.Fatal("peer completion did not join")
		}
	}
	select {
	case <-peerOwnerDone:
	case <-time.After(time.Second):
		t.Fatal("peer worker remained owned")
	}
	for _, o := range observers {
		s := o.state("channel_peer_wait")
		require.Equal(t, 1, s.ended)
		require.Equal(t, rpctrace.ResultOK, s.result)
	}
}

type callbackBoundaryObservationStore struct {
	gatedObservationStore
	truncate bool
}

func (s *callbackBoundaryObservationStore) Sync(ctx context.Context, mutations []Mutation) []MutationResult {
	results := s.gatedObservationStore.Sync(ctx, mutations)
	if s.truncate {
		return results[:1]
	}
	return results
}

// Sync has returned for the whole batch before completion A can block on
// downstream quorum work. Owner B's storage interval must already be closed.
func TestAppendObservationSyncEndsBeforeAnyCompletionCallback(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		name := "aligned"
		if mismatch {
			name = "cardinality_mismatch"
		}
		t.Run(name, func(t *testing.T) {
			owner, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &callbackBoundaryObservationStore{gatedObservationStore: gatedObservationStore{
				entered: make(chan struct{}), release: make(chan struct{}), failure: errors.New("owner-B-local"),
			}, truncate: mismatch}
			close(store.release)
			callbackA, callbackB, releaseA := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(releaseA) }) }
			t.Cleanup(unblock)
			runtime := &Runtime{ctx: owner}
			local := &runtimeLocalDurability{runtime: runtime, store: store, timeout: time.Second}
			pool, err := workqueue.NewBoundedBatchPool(workqueue.BoundedBatchPoolConfig[localDurabilityItem]{
				Name: "observed-callback", Workers: 1, QueueSize: 4, ReleaseTimeout: time.Second,
				Policy: func(localDurabilityItem) workqueue.BatchOptions {
					return workqueue.BatchOptions{MaxItems: 2, MaxWait: time.Second}
				},
			}, local.runBatch)
			require.NoError(t, err)
			runtime.localPool = pool
			t.Cleanup(func() { unblock(); require.NoError(t, pool.Close(context.Background())) })
			observers := []*gatedAppendObservation{newGatedAppendObservation(), newGatedAppendObservation()}
			type callbackResult struct {
				owner int
				value durabilityCompletion
			}
			completed := make(chan callbackResult, 2)
			for i, key := range []string{"1:callback-A", "1:callback-B"} {
				request := testReplicateRequest(t, ch.ChannelKey(key), name, 1, []byte("payload"))
				proposal := durableProposal{first: request.Manifest.BaseOffset + 1, last: request.Manifest.LastOffset,
					channelKey: request.ChannelKey, channelID: request.ChannelID, leader: request.Leader,
					manifest: request.Manifest, records: request.Records}
				ctx := rpctrace.WithAppendPhaseObserver(owner, observers[i])
				require.NoError(t, local.submitLocal(ctx, proposal, func(c durabilityCompletion) {
					if i == 0 {
						close(callbackA)
						<-releaseA
					} else {
						close(callbackB)
					}
					completed <- callbackResult{owner: i, value: c}
				}))
			}
			select {
			case <-callbackA:
			case <-time.After(time.Second):
				t.Fatal("first completion did not enter after Sync")
			}
			for i, observer := range observers {
				phase := observer.state(rpctrace.StageChannelLocalSync)
				require.Equal(t, 1, phase.begun)
				require.Equal(t, 1, phase.ended, "owner %d Sync already returned; another owner's callback cannot extend its span", i)
				expected := rpctrace.ResultOK
				if mismatch {
					expected = rpctrace.ResultUnfinished
				} else if i == 1 {
					expected = rpctrace.ResultError
				}
				require.Equal(t, expected, phase.result)
			}
			select {
			case <-callbackB:
				t.Fatal("completion order changed while owner A was blocked")
			default:
			}
			unblock()
			for i := range 2 {
				select {
				case c := <-completed:
					require.Equal(t, i, c.owner)
					if mismatch {
						require.ErrorIs(t, c.value.err, errInvalidExchangeResult)
						require.Equal(t, ch.AppendOutcomeUnknown, c.value.outcome)
					} else if i == 0 {
						require.NoError(t, c.value.err)
						require.Equal(t, ch.AppendOutcomeDurable, c.value.outcome)
					} else {
						require.ErrorIs(t, c.value.err, store.failure)
						require.Equal(t, ch.AppendOutcomeUnknown, c.value.outcome)
					}
				case <-time.After(time.Second):
					t.Fatal("completion did not join after release")
				}
			}
		})
	}
}
