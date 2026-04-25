package runtime

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
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
