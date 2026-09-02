package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/stretchr/testify/require"
)

func TestLoadingApplyMetaCoalescesSameFenceAndSupersedesOlderAuthority(t *testing.T) {
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: store.NewMemoryFactory(), MailboxSize: 8})
	base := testMeta("loading-meta-fence", 2, 1)
	original := NewFuture()
	loading := &storeLoadState{
		kind:       storeLoadApplyMeta,
		key:        base.Key,
		id:         base.ID,
		generation: 7,
		opID:       8,
		meta:       base,
		futures:    []*Future{original},
	}
	rc := &runtimeChannel{loading: loading}
	r.channels[base.Key] = rc

	sameFence := base
	sameFence.RetentionThroughSeq = 3
	sameFuture := NewFuture()
	r.handleApplyMetaToLoading(Event{Meta: sameFence, Future: sameFuture}, rc)
	require.Equal(t, base, loading.meta, "same authority must not replace the metadata snapshot being loaded")
	require.Equal(t, []*Future{original, sameFuture}, loading.futures)

	newer := base
	newer.LeaderEpoch++
	newer.Leader = 2
	newer.Replicas = []ch.NodeID{1, 2}
	newer.ISR = []ch.NodeID{1, 2}
	newerFuture := NewFuture()
	r.handleApplyMetaToLoading(Event{Meta: newer, Future: newerFuture}, rc)
	require.ErrorIs(t, awaitFutureError(t, original), ch.ErrStaleMeta)
	require.ErrorIs(t, awaitFutureError(t, sameFuture), ch.ErrStaleMeta)
	require.Equal(t, newer, loading.meta)
	require.Equal(t, []*Future{newerFuture}, loading.futures)

	staleFuture := NewFuture()
	r.handleApplyMetaToLoading(Event{Meta: base, Future: staleFuture}, rc)
	require.ErrorIs(t, awaitFutureError(t, staleFuture), ch.ErrStaleMeta)
	require.Equal(t, newer, loading.meta)
	require.Equal(t, []*Future{newerFuture}, loading.futures)
}

func TestColdActivationHintCoalescingPreservesLeaseAndNewestFence(t *testing.T) {
	baseMeta := testMeta("cold-coalesce", 2, 1)
	base := transport.PullHintRequest{
		ChannelKey:      baseMeta.Key,
		ChannelID:       baseMeta.ID,
		Epoch:           baseMeta.Epoch,
		LeaderEpoch:     baseMeta.LeaderEpoch,
		Leader:          baseMeta.Leader,
		LeaderLEO:       4,
		ActivityVersion: 5,
	}
	deadline := time.Unix(100, 0)
	loading := &storeLoadState{
		kind:      storeLoadColdActivation,
		key:       base.ChannelKey,
		id:        base.ChannelID,
		pullHint:  base,
		coldPhase: coldActivationResolve,
		deadline:  deadline,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: store.NewMemoryFactory(), MailboxSize: 8})
	rc := &runtimeChannel{loading: loading}
	r.channels[base.ChannelKey] = rc

	same := base
	same.LeaderLEO = 9
	same.ActivityVersion = 11
	require.NoError(t, r.coalesceColdActivationLoad(rc, same))
	require.Equal(t, uint64(9), loading.pullHint.LeaderLEO)
	require.Equal(t, uint64(11), loading.pullHint.ActivityVersion)
	require.Equal(t, deadline, loading.deadline)

	newer := same
	newer.LeaderEpoch++
	newer.Leader = 2
	require.NoError(t, r.coalesceColdActivationLoad(rc, newer))
	require.Equal(t, newer, loading.pullHint)
	require.Equal(t, deadline, loading.deadline)

	require.ErrorIs(t, r.coalesceColdActivationLoad(rc, base), ch.ErrStaleMeta)
	differentID := newer
	differentID.ChannelID.ID = "different"
	require.ErrorIs(t, r.coalesceColdActivationLoad(rc, differentID), ch.ErrStaleMeta)
	require.Equal(t, newer, loading.pullHint, "rejected hints must not poison the authority fence")
}

func TestColdAuthorityFailureReleasesCapacityAndExplicitWaiters(t *testing.T) {
	observer := &coldActivationRejectionObserver{}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: store.NewMemoryFactory(), MailboxSize: 8, Observer: observer})
	meta := testMeta("cold-authority-not-replica", 2, 1)
	ctx, cancel := context.WithCancel(context.Background())
	deadline := time.Now().Add(time.Hour)
	future := NewFuture()
	loading := &storeLoadState{
		kind:       storeLoadColdActivation,
		key:        meta.Key,
		id:         meta.ID,
		generation: 7,
		opID:       8,
		futures:    []*Future{future},
		coldPhase:  coldActivationResolve,
		deadline:   deadline,
		context:    ctx,
		cancel:     cancel,
	}
	rc := &runtimeChannel{loading: loading}
	r.channels[meta.Key] = rc
	r.scheduleColdActivationDeadline(loading)

	notReplica := meta
	notReplica.Replicas = []ch.NodeID{1}
	notReplica.ISR = []ch.NodeID{1}
	r.handleColdMetaResolveResult(worker.Result{
		Kind:        worker.TaskColdMetaResolve,
		Fence:       ch.Fence{ChannelKey: meta.Key, Generation: 7, OpID: 8},
		MetaResolve: &worker.MetaResolveResult{Meta: notReplica},
	})

	require.ErrorIs(t, awaitFutureError(t, future), ch.ErrNotReplica)
	require.NotContains(t, r.channels, meta.Key, "failed authority proof must return the channel capacity lease")
	require.Equal(t, "cold_not_replica", observer.reason)
	select {
	case <-ctx.Done():
	default:
		t.Fatal("failed authority proof must cancel the cold activation context")
	}
	_, scheduled := r.due.slots[dueSlot{kind: dueColdActivation, key: meta.Key}]
	require.False(t, scheduled)
}

func TestExplicitAuthorityBypassesColdResolveWithoutExtendingLease(t *testing.T) {
	factory := store.NewMemoryFactory()
	sink := captureCompletionSink{results: make(chan worker.Result, 4)}
	coldPool, err := worker.NewPool(
		worker.PoolConfig{Name: "cold-explicit-authority", Workers: 1, QueueSize: 4},
		worker.Deps{LocalNode: 2, Stores: factory},
		sink,
	)
	require.NoError(t, err)
	pools := &worker.Pools{ColdActivation: coldPool}
	defer pools.Close()

	meta := testMeta("cold-explicit-authority", 2, 1)
	oldContext, oldCancel := context.WithCancel(context.Background())
	deadline := time.Now().Add(time.Hour)
	future := NewFuture()
	loading := &storeLoadState{
		kind:       storeLoadColdActivation,
		key:        meta.Key,
		id:         meta.ID,
		generation: 7,
		opID:       8,
		coldPhase:  coldActivationResolve,
		deadline:   deadline,
		context:    oldContext,
		cancel:     oldCancel,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, Pools: pools, MailboxSize: 8})
	rc := &runtimeChannel{loading: loading}
	r.channels[meta.Key] = rc
	r.scheduleColdActivationDeadline(loading)

	r.handleApplyMetaToColdActivation(Event{Meta: meta, Future: future}, rc, loading, meta)

	require.Equal(t, coldActivationStoreLoad, loading.coldPhase)
	require.Equal(t, deadline, loading.deadline, "explicit authority must keep the original capacity-lease deadline")
	select {
	case <-oldContext.Done():
	default:
		t.Fatal("explicit authority must cancel the superseded metadata resolve")
	}
	result := sink.awaitResultKind(t, worker.TaskColdStoreLoad)
	require.Equal(t, loading.generation, result.Fence.Generation)
	require.Equal(t, loading.opID, result.Fence.OpID)
	r.handleStoreLoadResult(result)
	require.NoError(t, awaitFutureResult(t, future).Err)
	require.NotNil(t, rc.state)
	require.Nil(t, rc.loading)
}

func TestColdStoreLoadAcceptsOnlyCurrentFenceAndTransfersOwnership(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	meta := testMeta("cold-store-load-fence", 2, 1)
	staleStore, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	currentStore, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	staleHandle := staleStore.(*lifetimeTrackingStore)
	currentHandle := currentStore.(*lifetimeTrackingStore)
	t.Cleanup(func() { require.NoError(t, currentStore.Close()) })

	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: factory, MailboxSize: 8})
	ctx, cancel := context.WithCancel(context.Background())
	future := NewFuture()
	loading := &storeLoadState{
		kind:       storeLoadColdActivation,
		key:        meta.Key,
		id:         meta.ID,
		generation: 7,
		opID:       8,
		meta:       meta,
		futures:    []*Future{future},
		coldPhase:  coldActivationStoreLoad,
		deadline:   time.Now().Add(time.Hour),
		context:    ctx,
		cancel:     cancel,
	}
	rc := &runtimeChannel{loading: loading}
	r.channels[meta.Key] = rc
	r.scheduleColdActivationDeadline(loading)

	r.handleStoreLoadResult(worker.Result{
		Kind:      worker.TaskColdStoreLoad,
		Fence:     ch.Fence{ChannelKey: meta.Key, Generation: 7, OpID: 6},
		StoreLoad: &worker.StoreLoadResult{Store: staleStore},
	})
	require.Same(t, rc, r.channels[meta.Key])
	require.Same(t, loading, rc.loading)
	r.storeCloses.sealAndWait()
	require.Equal(t, int32(1), staleHandle.closeCalls.Load())

	r.handleStoreLoadResult(worker.Result{
		Kind:  worker.TaskColdStoreLoad,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: 7, OpID: 8},
		StoreLoad: &worker.StoreLoadResult{
			Store:   currentStore,
			Initial: store.InitialState{LEO: 4, HW: 3, CheckpointHW: 2},
			Retention: store.RetentionState{
				LocalRetentionThroughSeq:    1,
				PhysicalRetentionThroughSeq: 1,
			},
		},
	})

	require.NoError(t, awaitFutureResult(t, future).Err)
	require.Nil(t, rc.loading)
	require.Same(t, currentStore, rc.store)
	require.Equal(t, ch.RoleFollower, rc.state.Role)
	require.Equal(t, uint64(4), rc.state.LEO)
	require.Equal(t, uint64(3), rc.state.HW)
	require.Equal(t, uint64(2), rc.state.CheckpointHW)
	require.Equal(t, uint64(1), rc.state.LocalRetentionThroughSeq)
	require.Equal(t, uint64(1), rc.state.PhysicalRetentionThroughSeq)
	require.Equal(t, int32(0), currentHandle.closeCalls.Load(), "accepted handle ownership belongs to the loaded runtime")
	select {
	case <-ctx.Done():
	default:
		t.Fatal("accepted cold load must cancel the activation deadline context")
	}
	_, scheduled := r.due.slots[dueSlot{kind: dueColdActivation, key: meta.Key}]
	require.False(t, scheduled)
}

func TestColdActivationExpiryIsGenerationFenced(t *testing.T) {
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 2, Store: store.NewMemoryFactory(), MailboxSize: 8})
	meta := testMeta("cold-expiry-fence", 2, 1)
	deadline := time.Unix(200, 0)
	future := NewFuture()
	loading := &storeLoadState{
		kind:       storeLoadColdActivation,
		key:        meta.Key,
		id:         meta.ID,
		generation: 9,
		futures:    []*Future{future},
		deadline:   deadline,
	}
	rc := &runtimeChannel{loading: loading}
	r.channels[meta.Key] = rc

	r.releaseExpiredColdActivation(meta.Key, 8, deadline.Add(time.Second))
	require.Same(t, rc, r.channels[meta.Key])
	r.releaseExpiredColdActivation(meta.Key, 9, deadline.Add(-time.Nanosecond))
	require.Same(t, rc, r.channels[meta.Key])
	r.releaseExpiredColdActivation(meta.Key, 9, deadline)

	require.ErrorIs(t, awaitFutureError(t, future), context.DeadlineExceeded)
	require.NotContains(t, r.channels, meta.Key)
}
