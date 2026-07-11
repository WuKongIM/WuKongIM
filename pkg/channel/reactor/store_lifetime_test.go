package reactor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/stretchr/testify/require"
)

func TestGroupCloseReleasesLoadedStoreHandles(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = g.Close()
		}
	})

	for _, id := range []string{"close-loaded-a", "close-loaded-b"} {
		meta := testMeta(id, 1, 1)
		require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	}
	handles := factory.handlesSnapshot()
	require.Len(t, handles, 2)

	require.NoError(t, g.Close())
	closed = true
	for _, handle := range handles {
		require.Equal(t, int32(1), handle.closeCalls.Load(), "store %q was not released exactly once", handle.key)
	}
}

func TestReactorCloseFinalizesQueuedSuccessfulStoreLoad(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	id := ch.ChannelID{ID: "queued-store-load", Type: 1}
	key := ch.ChannelKeyForID(id)
	loaded, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	handle := loaded.(*lifetimeTrackingStore)

	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 4})
	future := NewFuture()
	require.NoError(t, r.SubmitCompletion(Event{
		Kind:   EventWorkerResult,
		Key:    key,
		Future: future,
		Worker: worker.Result{
			Kind:  worker.TaskStoreLoad,
			Fence: ch.Fence{ChannelKey: key, Generation: 1, OpID: 1},
			StoreLoad: &worker.StoreLoadResult{
				Store: loaded,
			},
		},
	}))
	r.once.Do(func() { close(r.stop) })
	r.start()
	require.NoError(t, r.Close())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = future.Await(ctx)
	require.ErrorIs(t, err, ch.ErrClosed)
	require.Eventually(t, func() bool {
		return handle.closeCalls.Load() == 1
	}, time.Second, time.Millisecond)
}

func TestReactorCloseWaitsForTrackedFallbackStoreClose(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	id := ch.ChannelID{ID: "tracked-fallback-close", Type: 1}
	key := ch.ChannelKeyForID(id)
	loaded, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	handle := loaded.(*lifetimeTrackingStore)
	handle.closeStarted = make(chan struct{}, 1)
	handle.closeBlock = make(chan struct{})
	var unblockOnce sync.Once
	t.Cleanup(func() { unblockOnce.Do(func() { close(handle.closeBlock) }) })

	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 4})
	state := machine.NewChannelState(key, 1, 1)
	state.ID = id
	r.channels[key] = &runtimeChannel{state: state, store: loaded}
	r.start()

	closeDone := make(chan error, 1)
	go func() { closeDone <- r.Close() }()
	select {
	case <-handle.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("fallback store close did not start")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Reactor.Close() returned before fallback close completed: %v", err)
	default:
	}

	unblockOnce.Do(func() { close(handle.closeBlock) })
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Reactor.Close() did not wait for fallback close")
	}
	require.Equal(t, int32(1), handle.closeCalls.Load())
}

func TestGroupCompleteClosesUndeliverableStoreLoadExactlyOnce(t *testing.T) {
	tests := []struct {
		name  string
		group func(t *testing.T) *Group
		key   ch.ChannelKey
	}{
		{name: "nil group", group: func(*testing.T) *Group { return nil }, key: "late-nil:1"},
		{name: "empty key", group: func(*testing.T) *Group { return &Group{} }},
		{name: "zero reactors", group: func(*testing.T) *Group { return &Group{} }, key: "late-zero:1"},
		{name: "closed group", group: func(t *testing.T) *Group {
			g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 4, Store: store.NewMemoryFactory()})
			require.NoError(t, err)
			require.NoError(t, g.Close())
			return g
		}, key: "late-closed:1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := newLifetimeTrackingStoreFactory()
			id := ch.ChannelID{ID: tt.name, Type: 1}
			loaded, err := factory.ChannelStore(tt.key, id)
			require.NoError(t, err)
			handle := loaded.(*lifetimeTrackingStore)
			result := worker.Result{
				Kind:  worker.TaskStoreLoad,
				Fence: ch.Fence{ChannelKey: tt.key, Generation: 1, OpID: 1},
				StoreLoad: &worker.StoreLoadResult{
					Store: loaded,
				},
			}

			tt.group(t).Complete(result)
			require.Equal(t, int32(1), handle.closeCalls.Load())
		})
	}
}

func TestStoreLoadDeliveryRacesGroupCloseExactlyOnce(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 4, Store: factory})
	require.NoError(t, err)
	id := ch.ChannelID{ID: "store-load-close-race", Type: 1}
	key := ch.ChannelKeyForID(id)
	loaded, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	handle := loaded.(*lifetimeTrackingStore)
	result := worker.Result{
		Kind:  worker.TaskStoreLoad,
		Fence: ch.Fence{ChannelKey: key, Generation: 1, OpID: 1},
		StoreLoad: &worker.StoreLoadResult{
			Store: loaded,
		},
	}

	start := make(chan struct{})
	deliveryDone := make(chan struct{})
	closeDone := make(chan error, 1)
	go func() {
		<-start
		g.Complete(result)
		close(deliveryDone)
	}()
	go func() {
		<-start
		closeDone <- g.Close()
	}()
	close(start)
	select {
	case <-deliveryDone:
	case <-time.After(time.Second):
		t.Fatal("StoreLoad delivery did not finish")
	}
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Group.Close() did not finish")
	}
	require.Equal(t, int32(1), handle.closeCalls.Load())
}

func TestGroupCloseWaitsForTrackedFallbackStoreClose(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	id := ch.ChannelID{ID: "group-tracked-fallback", Type: 1}
	key := ch.ChannelKeyForID(id)
	loaded, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	handle := loaded.(*lifetimeTrackingStore)
	handle.closeStarted = make(chan struct{}, 1)
	handle.closeBlock = make(chan struct{})
	var unblockOnce sync.Once
	t.Cleanup(func() { unblockOnce.Do(func() { close(handle.closeBlock) }) })

	tracker := newStoreCloseTracker()
	router, err := NewRouter(1)
	require.NoError(t, err)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 4, storeCloses: tracker})
	state := machine.NewChannelState(key, 1, 1)
	state.ID = id
	r.channels[key] = &runtimeChannel{state: state, store: loaded}
	r.start()
	g := &Group{
		cfg:         Config{LocalNode: 1, ReactorCount: 1, Store: factory, Observer: noopObserver{}},
		router:      router,
		reactors:    []*Reactor{r},
		storeCloses: tracker,
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- g.Close() }()
	select {
	case <-handle.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("group fallback close did not start")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Group.Close() returned before fallback close completed: %v", err)
	default:
	}

	unblockOnce.Do(func() { close(handle.closeBlock) })
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Group.Close() did not wait for fallback close")
	}
	require.Equal(t, int32(1), handle.closeCalls.Load())
}

func TestGroupCloseFailsLoadingFutureAndReleasesAcquiredHandle(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	meta := testMeta("close-loading", 1, 1)
	factory.blockLoad(meta.Key)
	defer factory.unblockLoad(meta.Key)
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 8, Store: factory})
	require.NoError(t, err)
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = g.Close()
		}
	})

	future, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	factory.waitLoadStarted(t, meta.Key)
	closeDone := make(chan error, 1)
	go func() { closeDone <- g.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = future.Await(ctx)
	require.ErrorIs(t, err, ch.ErrClosed)
	select {
	case err := <-closeDone:
		require.NoError(t, err)
		closed = true
	case <-time.After(time.Second):
		t.Fatal("Group.Close() did not cancel the running store load")
	}
	handles := factory.handlesSnapshot()
	require.Len(t, handles, 1)
	require.Equal(t, int32(1), handles[0].closeCalls.Load())
}

func TestReactorCloseReleasesPendingMetaStoreHandle(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	id := ch.ChannelID{ID: "close-pending-meta", Type: 1}
	key := ch.ChannelKeyForID(id)
	loaded, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	handle := loaded.(*lifetimeTrackingStore)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 4})
	r.channels[key] = &runtimeChannel{
		store: loaded,
		pending: &pendingMetaState{
			key:        key,
			id:         id,
			generation: 1,
		},
	}
	r.pendingMetaCount = 1
	r.start()

	require.NoError(t, r.Close())
	require.Equal(t, int32(1), handle.closeCalls.Load())
	require.Empty(t, r.channels)
	require.Zero(t, r.pendingMetaCount)
}

func TestFailedApplyMetaLoadClosesStoreDeletesShellAndFreesCapacity(t *testing.T) {
	factory := newLifetimeTrackingStoreFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 8, MaxChannels: 1, Store: factory})
	require.NoError(t, err)
	defer g.Close()

	invalid := testMeta("invalid-loaded-meta", 1, 1)
	invalid.MinISR = len(invalid.ISR) + 1
	err = awaitSubmit(g, invalid.Key, Event{Kind: EventApplyMeta, Key: invalid.Key, Meta: invalid})
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Eventually(t, func() bool {
		handles := factory.handlesSnapshot()
		return len(handles) == 1 && handles[0].closeCalls.Load() == 1
	}, time.Second, time.Millisecond)
	hasState, err := g.HasChannelState(context.Background(), invalid.Key)
	require.NoError(t, err)
	require.False(t, hasState)

	valid := testMeta("valid-after-failed-load", 1, 1)
	require.NoError(t, awaitSubmit(g, valid.Key, Event{Kind: EventApplyMeta, Key: valid.Key, Meta: valid}))
}

func TestLoadChannelStoreClosesHandleOnLoadAndRetentionFailure(t *testing.T) {
	wantErr := errors.New("load channel store failed")
	tests := []struct {
		name      string
		configure func(*lifetimeTrackingStoreFactory)
	}{
		{name: "load", configure: func(factory *lifetimeTrackingStoreFactory) { factory.loadErr = wantErr }},
		{name: "retention", configure: func(factory *lifetimeTrackingStoreFactory) { factory.retentionErr = wantErr }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := newLifetimeTrackingStoreFactory()
			tt.configure(factory)
			r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 4})
			id := ch.ChannelID{ID: "load-failure-" + tt.name, Type: 1}
			loaded, _, err := r.loadChannelStore(ch.ChannelKeyForID(id), id)
			require.ErrorIs(t, err, wantErr)
			require.Nil(t, loaded)
			handles := factory.handlesSnapshot()
			require.Len(t, handles, 1)
			require.Equal(t, int32(1), handles[0].closeCalls.Load())
		})
	}
}

func TestRuntimeEvictReleasesMessageDBLeaseAndReloadPreservesState(t *testing.T) {
	factory := store.NewMessageDBFactory(t.TempDir())
	defer factory.Close()
	g, err := NewGroup(Config{
		LocalNode:             1,
		ReactorCount:          1,
		MailboxSize:           16,
		Store:                 factory,
		AppendBatchMaxRecords: 1,
	})
	require.NoError(t, err)
	defer g.Close()

	meta := testMeta("message-db-evict-reload", 1, 1)
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	appendFuture, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1001, "preserved"))
	require.NoError(t, err)
	appendResult := awaitFutureResult(t, appendFuture)
	require.NoError(t, appendResult.Err)
	require.Equal(t, uint64(1), appendResult.AppendBatch.Items[0].MessageSeq)

	checkpointStore, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	require.NoError(t, checkpointStore.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 1}))
	require.NoError(t, checkpointStore.Close())

	evicted, err := g.RuntimeEvict(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.Equal(t, 1, evicted.Evicted)
	require.Eventually(t, func() bool {
		probe, err := factory.ChannelStore(meta.Key, ch.ChannelID{ID: "different-id-probe", Type: 127})
		if err != nil {
			return false
		}
		return probe.Close() == nil
	}, time.Second, time.Millisecond)

	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	probe, err := g.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.Len(t, probe.Channels, 1)
	require.Equal(t, uint64(1), probe.Channels[0].LEO)
	require.Equal(t, uint64(1), probe.Channels[0].HW)
	require.Equal(t, uint64(1), probe.Channels[0].CheckpointHW)

	readStore, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	read, readErr := readStore.ReadCommitted(context.Background(), store.ReadCommittedRequest{
		FromSeq: 1, MaxSeq: 1, Limit: 1, MaxBytes: 1024,
	})
	closeErr := readStore.Close()
	require.NoError(t, readErr)
	require.NoError(t, closeErr)
	require.Len(t, read.Messages, 1)
	require.Equal(t, uint64(1001), read.Messages[0].MessageID)
	require.Equal(t, []byte("preserved"), read.Messages[0].Payload)
}

// lifetimeTrackingStoreFactory returns a fresh wrapper for every acquisition so
// lifecycle tests observe lease ownership instead of reusing one test handle.
type lifetimeTrackingStoreFactory struct {
	base *store.MemoryFactory

	mu           sync.Mutex
	handles      []*lifetimeTrackingStore
	loadBlocks   map[ch.ChannelKey]chan struct{}
	loadStarted  map[ch.ChannelKey]chan struct{}
	loadErr      error
	retentionErr error
}

func newLifetimeTrackingStoreFactory() *lifetimeTrackingStoreFactory {
	return &lifetimeTrackingStoreFactory{
		base:        store.NewMemoryFactory(),
		loadBlocks:  make(map[ch.ChannelKey]chan struct{}),
		loadStarted: make(map[ch.ChannelKey]chan struct{}),
	}
}

func (f *lifetimeTrackingStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	handle := &lifetimeTrackingStore{ChannelStore: base, factory: f, key: key}
	f.mu.Lock()
	f.handles = append(f.handles, handle)
	f.mu.Unlock()
	return handle, nil
}

func (f *lifetimeTrackingStoreFactory) blockLoad(key ch.ChannelKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadBlocks[key] = make(chan struct{})
	f.loadStarted[key] = make(chan struct{}, 1)
}

func (f *lifetimeTrackingStoreFactory) unblockLoad(key ch.ChannelKey) {
	f.mu.Lock()
	block := f.loadBlocks[key]
	delete(f.loadBlocks, key)
	f.mu.Unlock()
	if block != nil {
		close(block)
	}
}

func (f *lifetimeTrackingStoreFactory) waitLoadStarted(t *testing.T, key ch.ChannelKey) {
	t.Helper()
	f.mu.Lock()
	started := f.loadStarted[key]
	f.mu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for store load")
	}
}

func (f *lifetimeTrackingStoreFactory) loadControl(key ch.ChannelKey) (<-chan struct{}, chan<- struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loadBlocks[key], f.loadStarted[key]
}

func (f *lifetimeTrackingStoreFactory) handlesSnapshot() []*lifetimeTrackingStore {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*lifetimeTrackingStore(nil), f.handles...)
}

type lifetimeTrackingStore struct {
	store.ChannelStore
	factory      *lifetimeTrackingStoreFactory
	key          ch.ChannelKey
	closeCalls   atomic.Int32
	closeStarted chan struct{}
	closeBlock   chan struct{}
}

func (s *lifetimeTrackingStore) Close() error {
	s.closeCalls.Add(1)
	if s.closeStarted != nil {
		select {
		case s.closeStarted <- struct{}{}:
		default:
		}
	}
	if s.closeBlock != nil {
		<-s.closeBlock
	}
	return s.ChannelStore.Close()
}

func (s *lifetimeTrackingStore) Load(ctx context.Context) (store.InitialState, error) {
	block, started := s.factory.loadControl(s.key)
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return store.InitialState{}, ctx.Err()
		}
	}
	if s.factory.loadErr != nil {
		return store.InitialState{}, s.factory.loadErr
	}
	return s.ChannelStore.Load(ctx)
}

func (s *lifetimeTrackingStore) LoadRetentionState(ctx context.Context) (store.RetentionState, error) {
	if s.factory.retentionErr != nil {
		return store.RetentionState{}, s.factory.retentionErr
	}
	return s.ChannelStore.LoadRetentionState(ctx)
}
