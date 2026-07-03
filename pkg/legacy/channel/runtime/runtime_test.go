package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestChannelLookupUsesReadOnlyPath(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-1")
	require.NoError(t, rt.EnsureChannel(meta))

	ch, ok := rt.Channel(meta.Key)
	require.True(t, ok)
	require.Equal(t, meta.Key, ch.ID())
}

func TestRetentionEffectiveMinAvailableSeqUsesHighestFence(t *testing.T) {
	require.Equal(t, uint64(1), core.EffectiveMinAvailableSeq(0, 0))
	require.Equal(t, uint64(9), core.EffectiveMinAvailableSeq(8, 3))
	require.Equal(t, uint64(12), core.EffectiveMinAvailableSeq(8, 11))
	require.Equal(t, ^uint64(0), core.EffectiveMinAvailableSeq(^uint64(0), 11))
}

func TestRetentionMetaEqualityIncludesRetentionBoundary(t *testing.T) {
	current := testMeta("room-retention-equality")
	next := current
	next.RetentionThroughSeq = 88

	require.False(t, metaEqualExceptLease(current, next))
}

func TestApplyMetaSerializesConcurrentWriteFenceUpdates(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-fence-serialize")
	require.NoError(t, rt.EnsureChannel(meta))
	rep := rt.replicaForTest(t, meta.Key)
	applyEntered := make(chan struct{})
	releaseApply := make(chan struct{})
	rep.blockApplyMetaFenceVersion = 1
	rep.applyMetaEntered = applyEntered
	rep.releaseApplyMeta = releaseApply

	older := meta
	older.WriteFence = core.WriteFence{
		Token:   "task-1",
		Version: 1,
		Reason:  core.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	newer := meta
	newer.WriteFence = core.WriteFence{
		Token:   "task-2",
		Version: 2,
		Reason:  core.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}

	olderDone := make(chan error, 1)
	go func() {
		olderDone <- rt.ApplyMeta(older)
	}()
	<-applyEntered

	newerDone := make(chan error, 1)
	go func() {
		newerDone <- rt.ApplyMeta(newer)
	}()
	newerFinished := false
	select {
	case err := <-newerDone:
		require.NoError(t, err)
		newerFinished = true
	case <-time.After(25 * time.Millisecond):
	}

	close(releaseApply)
	require.NoError(t, <-olderDone)
	if !newerFinished {
		select {
		case err := <-newerDone:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("newer ApplyMeta did not finish")
		}
	}

	ch, ok := rt.Channel(meta.Key)
	require.True(t, ok)
	require.Equal(t, uint64(2), ch.Meta().WriteFence.Version)
	require.Equal(t, "task-2", ch.Meta().WriteFence.Token)
}

func TestChannelAppendRejectsLocalWriteFence(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-runtime-fence")
	require.NoError(t, rt.EnsureChannel(meta))
	fenced := meta
	fenced.WriteFence = core.WriteFence{
		Token:   "task-runtime-fence",
		Version: 1,
		Reason:  core.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, rt.ApplyMeta(fenced))

	ch, ok := rt.Channel(meta.Key)
	require.True(t, ok)
	_, err := ch.Append(context.Background(), []core.Record{{Payload: []byte("x"), SizeBytes: 1}})
	require.ErrorIs(t, err, core.ErrWriteFenced)
	require.Equal(t, 0, rt.replicaForTest(t, meta.Key).appendCalls)
}

func TestFenceAndDrainReturnsRuntimeGeneration(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-runtime-drain")
	fenced := meta
	fenced.WriteFence = core.WriteFence{
		Token:   "task-runtime-drain",
		Version: 3,
		Reason:  core.WriteFenceReasonMigration,
		Until:   time.Now().Add(time.Minute),
	}
	require.NoError(t, rt.EnsureChannel(fenced))

	drain, err := rt.FenceAndDrain(context.Background(), core.FenceAndDrainRequest{
		ChannelKey:           meta.Key,
		WriteFenceToken:      "task-runtime-drain",
		WriteFenceVersion:    3,
		ExpectedChannelEpoch: meta.Epoch,
		ExpectedLeader:       meta.Leader,
	})
	require.NoError(t, err)
	require.Equal(t, meta.Key, drain.ChannelKey)
	require.Equal(t, uint64(3), drain.WriteFenceVersion)
	require.Equal(t, uint64(1), drain.RuntimeGeneration)
}

func TestRuntimeApplyRetentionBoundaryDelegatesToReplica(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-retention-apply")
	require.NoError(t, rt.EnsureChannel(meta))

	require.NoError(t, rt.ApplyRetentionBoundary(context.Background(), meta.Key, 9))

	rep := rt.replicaForTest(t, meta.Key)
	require.Equal(t, []uint64{9}, rep.retentionCalls)
	require.Equal(t, uint64(9), rep.Status().RetentionThroughSeq)
}

func TestRuntimeRetentionViewDelegatesToReplica(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-retention-view")
	require.NoError(t, rt.EnsureChannel(meta))
	rep := rt.replicaForTest(t, meta.Key)
	rep.retentionView = core.RetentionView{
		ChannelKey:          meta.Key,
		Epoch:               7,
		Leader:              1,
		RetentionThroughSeq: 8,
		MinAvailableSeq:     9,
		MinISRMatchOffset:   4,
	}

	view, err := rt.RetentionView(meta.Key)

	require.NoError(t, err)
	require.Equal(t, rep.retentionView, view)
}

func TestEnsureChannel(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-ensure")

	require.NoError(t, rt.EnsureChannel(meta))

	_, ok := rt.Channel(meta.Key)
	require.True(t, ok)
	store, ok := rt.generationStore.(*fakeGenerationStore)
	require.True(t, ok)
	require.Equal(t, uint64(1), store.stored[meta.Key])
}

func TestRuntimeIdleEvictionRemovesIdleChannelAndReleasesLimit(t *testing.T) {
	now := time.Unix(1700000000, 0)
	factory := newFakeReplicaFactory()
	evictedKeys := make([]core.ChannelKey, 0, 1)
	created, err := New(Config{
		LocalNode:       1,
		ReplicaFactory:  factory,
		GenerationStore: newFakeGenerationStore(),
		Limits: Limits{
			MaxChannels: 1,
		},
		IdleEviction: IdleEvictionPolicy{
			IdleTimeout: time.Second,
		},
		OnIdleEvict: func(key core.ChannelKey) {
			evictedKeys = append(evictedKeys, key)
		},
		Tombstones: TombstonePolicy{
			TombstoneTTL:    30 * time.Second,
			CleanupInterval: 30 * time.Second,
		},
		Now: func() time.Time { return now },
	})
	require.NoError(t, err)
	rt := created.(*runtime)
	t.Cleanup(func() { require.NoError(t, rt.Close()) })

	first := testMeta("room-idle-evict-1")
	require.NoError(t, rt.EnsureChannel(first))

	now = now.Add(2 * time.Second)
	require.Equal(t, 1, rt.evictIdleChannels())
	_, ok := rt.Channel(first.Key)
	require.False(t, ok)
	require.Equal(t, 0, rt.totalChannels())
	require.Equal(t, 1, factory.replicas[0].closeCalls())
	require.Equal(t, []core.ChannelKey{first.Key}, evictedKeys)

	require.NoError(t, rt.EnsureChannel(testMeta("room-idle-evict-2")))
	require.Equal(t, 1, rt.totalChannels())
}

func TestRuntimeIdleEvictionKeepsRecentlyTouchedChannel(t *testing.T) {
	now := time.Unix(1700000000, 0)
	created, err := New(Config{
		LocalNode:       1,
		ReplicaFactory:  newFakeReplicaFactory(),
		GenerationStore: newFakeGenerationStore(),
		IdleEviction: IdleEvictionPolicy{
			IdleTimeout: time.Second,
		},
		Tombstones: TombstonePolicy{
			TombstoneTTL:    30 * time.Second,
			CleanupInterval: 30 * time.Second,
		},
		Now: func() time.Time { return now },
	})
	require.NoError(t, err)
	rt := created.(*runtime)
	t.Cleanup(func() { require.NoError(t, rt.Close()) })

	meta := testMeta("room-idle-touch")
	require.NoError(t, rt.EnsureChannel(meta))
	now = now.Add(900 * time.Millisecond)
	handle, ok := rt.Channel(meta.Key)
	require.True(t, ok)
	_, err = handle.Append(context.Background(), []core.Record{{Payload: []byte("x"), SizeBytes: 1}})
	require.NoError(t, err)

	now = now.Add(600 * time.Millisecond)
	require.Equal(t, 0, rt.evictIdleChannels())
	_, ok = rt.Channel(meta.Key)
	require.True(t, ok)
}

func TestRuntimeCleanupRespectsIdleScanIntervalWhenTombstoneCleanupIsFaster(t *testing.T) {
	var tombstoneDrops atomic.Int64
	var idleEvicts atomic.Int64
	created, err := New(Config{
		LocalNode:       1,
		ReplicaFactory:  newFakeReplicaFactory(),
		GenerationStore: newFakeGenerationStore(),
		IdleEviction: IdleEvictionPolicy{
			IdleTimeout:  time.Millisecond,
			ScanInterval: 2 * time.Second,
		},
		OnIdleEvict: func(core.ChannelKey) {
			idleEvicts.Add(1)
		},
		Tombstones: TombstonePolicy{
			TombstoneTTL:    time.Second,
			CleanupInterval: 5 * time.Millisecond,
		},
	})
	require.NoError(t, err)
	rt := created.(*runtime)
	rt.tombstones.setHooks(nil, func() {
		tombstoneDrops.Add(1)
	})
	t.Cleanup(func() { require.NoError(t, rt.Close()) })

	meta := testMeta("room-idle-scan-interval")
	require.NoError(t, rt.EnsureChannel(meta))
	require.Eventually(t, func() bool {
		return tombstoneDrops.Load() >= 2
	}, 300*time.Millisecond, 5*time.Millisecond)
	require.Zero(t, idleEvicts.Load())
	_, ok := rt.Channel(meta.Key)
	require.True(t, ok)
}

func TestEnsureChannelLeaderLocalAppendNotifierDoesNotBlockCaller(t *testing.T) {
	store := newFakeGenerationStore()
	factory := newFakeReplicaFactory()
	created, err := New(Config{
		LocalNode:           1,
		ReplicaFactory:      factory,
		GenerationStore:     store,
		LongPollLaneCount:   4,
		LongPollMaxWait:     time.Second,
		LongPollMaxBytes:    64 * 1024,
		LongPollMaxChannels: 64,
		Tombstones: TombstonePolicy{
			TombstoneTTL: 30 * time.Second,
		},
		Now: time.Now,
	})
	require.NoError(t, err)
	rt := created.(*runtime)
	t.Cleanup(func() {
		rt.stopTombstoneCleanup()
	})

	meta := testMeta("room-local-append-async")
	require.NoError(t, rt.EnsureChannel(meta))

	factory.mu.Lock()
	require.Len(t, factory.replicas, 1)
	rep := factory.replicas[0]
	factory.mu.Unlock()

	rep.mu.Lock()
	notify := rep.onLeaderLocalAppend
	rep.mu.Unlock()
	require.NotNil(t, notify)

	shard := rt.shardFor(meta.Key)
	shard.mu.Lock()
	done := make(chan struct{})
	go func() {
		notify()
		close(done)
	}()

	select {
	case <-done:
		shard.mu.Unlock()
	case <-time.After(25 * time.Millisecond):
		shard.mu.Unlock()
		<-done
		t.Fatal("leader local append notifier blocked the replica caller")
	}
}

func TestEnsureChannelSkipsLongPollNotifiersWhenLongPollDisabled(t *testing.T) {
	store := newFakeGenerationStore()
	factory := newFakeReplicaFactory()
	created, err := New(Config{
		LocalNode:       1,
		ReplicaFactory:  factory,
		GenerationStore: store,
		Tombstones: TombstonePolicy{
			TombstoneTTL: 30 * time.Second,
		},
		Now: time.Now,
	})
	require.NoError(t, err)
	rt := created.(*runtime)
	t.Cleanup(func() {
		rt.stopTombstoneCleanup()
	})

	meta := testMeta("room-no-longpoll-notifiers")
	require.NoError(t, rt.EnsureChannel(meta))

	factory.mu.Lock()
	require.Len(t, factory.replicas, 1)
	rep := factory.replicas[0]
	factory.mu.Unlock()

	rep.mu.Lock()
	defer rep.mu.Unlock()
	require.Nil(t, rep.onLeaderLocalAppend)
	require.Nil(t, rep.onLeaderHWAdvance)
}

func TestRemoveChannel(t *testing.T) {
	rt := newTestRuntime(t)
	meta := testMeta("room-remove")
	require.NoError(t, rt.EnsureChannel(meta))

	require.NoError(t, rt.RemoveChannel(meta.Key))

	_, ok := rt.Channel(meta.Key)
	require.False(t, ok)
}

func TestRemoveChannelPublishesTombstoneBeforeRegistryDrop(t *testing.T) {
	meta := testMeta("room-remove-visibility")
	blockAdd := make(chan struct{})
	addStarted := make(chan struct{})
	rt := newTestRuntimeWithOptions(t, withTombstoneAddHook(func() {
		close(addStarted)
		<-blockAdd
	}))
	require.NoError(t, rt.EnsureChannel(meta))

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- rt.RemoveChannel(meta.Key)
	}()

	<-addStarted
	require.False(t, rt.tombstones.contains(meta.Key, 1))

	lookupDone := make(chan bool, 1)
	go func() {
		_, ok := rt.Channel(meta.Key)
		lookupDone <- ok
	}()

	select {
	case ok := <-lookupDone:
		require.True(t, ok, "channel became invisible before tombstone was published")
	case <-time.After(50 * time.Millisecond):
	}

	close(blockAdd)
	require.NoError(t, <-removeDone)
	_, ok := rt.Channel(meta.Key)
	require.False(t, ok)
	require.True(t, rt.tombstones.contains(meta.Key, 1))
}

func TestEnsureChannelIsAtomicPerKey(t *testing.T) {
	rt := newTestRuntimeWithOptions(t,
		withGenerationStoreDelay(25*time.Millisecond),
		withReplicaFactoryDelay(25*time.Millisecond),
	)
	meta := testMeta("room-atomic")

	const callers = 8
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			<-start
			errs <- rt.EnsureChannel(meta)
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	var success int
	var exists int
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrChannelExists):
			exists++
		default:
			require.NoError(t, err)
		}
	}

	require.Equal(t, 1, success)
	require.Equal(t, callers-1, exists)

	store, ok := rt.generationStore.(*fakeGenerationStore)
	require.True(t, ok)
	require.Equal(t, uint64(1), store.stored[meta.Key])

	factory, ok := rt.replicaFactory.(*fakeReplicaFactory)
	require.True(t, ok)
	require.Len(t, factory.created, 1)
	require.Equal(t, uint64(1), factory.created[0].Generation)
}

func TestEnsureChannelRespectsMaxChannelsUnderConcurrency(t *testing.T) {
	const maxChannels = 3
	keys := distinctShardKeys(t, 10)
	rt := newTestRuntimeWithOptions(
		t,
		withMaxChannels(maxChannels),
		withGenerationStoreDelay(25*time.Millisecond),
		withReplicaFactoryDelay(25*time.Millisecond),
	)

	start := make(chan struct{})
	errs := make(chan error, len(keys))
	var wg sync.WaitGroup
	wg.Add(len(keys))
	for _, key := range keys {
		meta := testMeta(string(key))
		go func(meta core.Meta) {
			defer wg.Done()
			<-start
			errs <- rt.EnsureChannel(meta)
		}(meta)
	}

	close(start)
	wg.Wait()
	close(errs)

	var success int
	var tooMany int
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrTooManyChannels):
			tooMany++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, maxChannels, success)
	require.Equal(t, len(keys)-maxChannels, tooMany)
	require.Equal(t, maxChannels, rt.totalChannels())

	factory, ok := rt.replicaFactory.(*fakeReplicaFactory)
	require.True(t, ok)
	require.Len(t, factory.created, maxChannels)
}

func TestEnsureChannelNotifiesMaxChannelRejection(t *testing.T) {
	rejected := make([]core.ChannelKey, 0, 1)
	rt := newTestRuntimeWithOptions(
		t,
		withMaxChannels(1),
		withActivationRejectHook(func(key core.ChannelKey, err error) {
			if errors.Is(err, ErrTooManyChannels) {
				rejected = append(rejected, key)
			}
		}),
	)

	require.NoError(t, rt.EnsureChannel(testMeta("room-cap-1")))
	second := testMeta("room-cap-2")
	require.ErrorIs(t, rt.EnsureChannel(second), ErrTooManyChannels)
	require.Equal(t, []core.ChannelKey{second.Key}, rejected)
}

func TestRemoveChannelTombstoneFailureKeepsChannel(t *testing.T) {
	meta := testMeta("room-remove-failure")
	rt := newTestRuntimeWithOptions(t, withTombstoneError(meta.Key, errors.New("boom")))
	require.NoError(t, rt.EnsureChannel(meta))

	err := rt.RemoveChannel(meta.Key)
	require.Error(t, err)

	ch, ok := rt.Channel(meta.Key)
	require.True(t, ok)
	require.Equal(t, meta.Key, ch.ID())
	require.False(t, rt.tombstones.contains(meta.Key, 1))
}

func TestRuntimeCloseStopsTombstoneCleanupWorker(t *testing.T) {
	var drops atomic.Int64
	rt := newTestRuntimeWithOptions(
		t,
		withTombstoneTTL(time.Second),
		withTombstoneCleanupInterval(5*time.Millisecond),
		withTombstoneDropHook(func() {
			drops.Add(1)
		}),
	)
	require.Eventually(t, func() bool {
		return drops.Load() > 0
	}, 300*time.Millisecond, 10*time.Millisecond)

	require.NoError(t, rt.Close())
	stoppedAt := drops.Load()
	time.Sleep(40 * time.Millisecond)
	require.Equal(t, stoppedAt, drops.Load())
}

func TestRemoveChannelClosesReplica(t *testing.T) {
	meta := testMeta("room-remove-close")
	rt := newTestRuntime(t)
	require.NoError(t, rt.EnsureChannel(meta))

	require.NoError(t, rt.RemoveChannel(meta.Key))

	factory, ok := rt.replicaFactory.(*fakeReplicaFactory)
	require.True(t, ok)
	require.Len(t, factory.replicas, 1)
	require.Equal(t, 1, factory.replicas[0].closeCalls())
}

func TestEnsureChannelFailureAfterReplicaCreationClosesReplica(t *testing.T) {
	meta := testMeta("room-ensure-close")
	rt := newTestRuntimeWithOptions(t, withBecomeLeaderError(meta.Key, errors.New("become leader failed")))

	err := rt.EnsureChannel(meta)
	require.Error(t, err)

	factory, ok := rt.replicaFactory.(*fakeReplicaFactory)
	require.True(t, ok)
	require.Len(t, factory.replicas, 1)
	require.Equal(t, 1, factory.replicas[0].closeCalls())
	_, exists := rt.Channel(meta.Key)
	require.False(t, exists)
}

func distinctShardKeys(t *testing.T, n int) []core.ChannelKey {
	t.Helper()

	keys := make([]core.ChannelKey, 0, n)
	seen := make(map[uint32]struct{}, n)
	for i := 0; i < 10000 && len(keys) < n; i++ {
		key := core.ChannelKey(fmt.Sprintf("room-limit-%d", i))
		idx := shardIndex(key)
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		keys = append(keys, key)
	}
	require.Len(t, keys, n)
	return keys
}
