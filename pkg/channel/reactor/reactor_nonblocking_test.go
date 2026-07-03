package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/stretchr/testify/require"
)

func TestSlowObserverSeesSlowEvent(t *testing.T) {
	obs := &slowPathObserver{}
	r := NewReactor(ReactorConfig{ID: 7, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16, Observer: obs, SlowEventThreshold: time.Nanosecond})
	future := NewFuture()
	future.beforeComplete = func(Result) {
		time.Sleep(time.Millisecond)
	}

	r.handle(Event{Kind: EventCheckState, Key: "missing", Future: future})

	require.Equal(t, []slowEvent{{reactorID: 7, kind: "EventCheckState"}}, obs.Events())
}

func TestSlowObserverSeesSlowDueItem(t *testing.T) {
	obs := &slowPathObserver{}
	r := NewReactor(ReactorConfig{ID: 8, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16, Observer: obs, SlowDueThreshold: time.Nanosecond})
	meta := testMeta("slow-due", 1, 1)
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	event := appendEvent(meta, 1, "payload")
	event.Context = ctx
	event.Future.beforeComplete = func(Result) {
		time.Sleep(time.Millisecond)
	}
	req := newAppendRequest(event, time.Now())
	require.NoError(t, r.enqueueAppendRequest(rc, req))
	r.registerAppendCancelContext(rc, event.OpID, ctx)

	r.processDueItem(dueItem{key: meta.Key, kind: dueAppendFlush, version: rc.appendFlushDueVersion}, time.Now())

	require.Equal(t, []slowDue{{reactorID: 8, kind: "dueAppendFlush"}}, obs.DueItems())
}

func TestApplyMetaLoadDoesNotBlockStartedReactor(t *testing.T) {
	factory := newBlockingLoadStoreFactory()
	meta := testMeta("async-load", 1, 1)
	factory.blockLoad(meta.Key)
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()
	defer factory.unblockLoad(meta.Key)

	applyFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	factory.waitLoadStarted(t, meta.Key)
	requireFuturePending(t, applyFuture)

	checkCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	hasState, err := g.HasChannelState(checkCtx, meta.Key)
	require.NoError(t, err)
	require.False(t, hasState)

	factory.unblockLoad(meta.Key)
	result, err := applyFuture.Await(context.Background())
	require.NoError(t, err)
	require.NoError(t, result.Err)
}

func TestPullHintPendingMetaLoadDoesNotBlockStartedReactor(t *testing.T) {
	factory := newBlockingLoadStoreFactory()
	meta := testMeta("async-pending-meta-load", 1, 2)
	factory.blockLoad(meta.Key)
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()
	defer factory.unblockLoad(meta.Key)

	future, err := g.Submit(context.Background(), meta.Key, Event{
		Kind: EventPullHint,
		Key:  meta.Key,
		PullHint: transport.PullHintRequest{
			ChannelKey:      meta.Key,
			ChannelID:       meta.ID,
			Epoch:           meta.Epoch,
			LeaderEpoch:     meta.LeaderEpoch,
			Leader:          meta.Leader,
			LeaderLEO:       3,
			ActivityVersion: 3,
		},
	})
	require.NoError(t, err)
	factory.waitLoadStarted(t, meta.Key)

	awaitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := future.Await(awaitCtx)
	require.NoError(t, err)
	require.NoError(t, result.Err)
}

func TestRuntimeEvictDoesNotBlockOnStoreClose(t *testing.T) {
	factory := newBlockingLoadStoreFactory()
	meta := testMeta("async-close", 1, 1)
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	factory.blockClose(meta.Key)
	defer factory.unblockClose(meta.Key)
	evictCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := g.RuntimeEvict(evictCtx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Evicted)

	factory.unblockClose(meta.Key)
	factory.waitCloseStarted(t, meta.Key)
}

type slowEvent struct {
	reactorID int
	kind      string
}

type slowDue struct {
	reactorID int
	kind      string
}

type slowPathObserver struct {
	captureObserver
	mu    sync.Mutex
	event []slowEvent
	due   []slowDue
}

func (o *slowPathObserver) ObserveReactorSlowEvent(reactorID int, kind string, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.event = append(o.event, slowEvent{reactorID: reactorID, kind: kind})
}

func (o *slowPathObserver) ObserveReactorSlowDue(reactorID int, kind string, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.due = append(o.due, slowDue{reactorID: reactorID, kind: kind})
}

func (o *slowPathObserver) Events() []slowEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]slowEvent(nil), o.event...)
}

func (o *slowPathObserver) DueItems() []slowDue {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]slowDue(nil), o.due...)
}

type blockingLoadStoreFactory struct {
	base   *store.MemoryFactory
	mu     sync.Mutex
	stores map[ch.ChannelKey]*blockingLoadStore
}

func newBlockingLoadStoreFactory() *blockingLoadStoreFactory {
	return &blockingLoadStoreFactory{base: store.NewMemoryFactory(), stores: make(map[ch.ChannelKey]*blockingLoadStore)}
}

func (f *blockingLoadStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing := f.stores[key]; existing != nil {
		if existing.base == nil {
			base, err := f.base.ChannelStore(key, id)
			if err != nil {
				return nil, err
			}
			existing.base = base
		}
		return existing, nil
	}
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	cs := &blockingLoadStore{factory: f, key: key, base: base}
	f.stores[key] = cs
	return cs, nil
}

func (f *blockingLoadStoreFactory) blockLoad(key ch.ChannelKey) {
	cs := f.mustStore(key)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.loadBlock = make(chan struct{})
	cs.loadStarted = make(chan struct{}, 1)
}

func (f *blockingLoadStoreFactory) unblockLoad(key ch.ChannelKey) {
	cs := f.mustStore(key)
	cs.mu.Lock()
	block := cs.loadBlock
	cs.loadBlock = nil
	cs.mu.Unlock()
	if block != nil {
		close(block)
	}
}

func (f *blockingLoadStoreFactory) waitLoadStarted(t *testing.T, key ch.ChannelKey) {
	t.Helper()
	cs := f.mustStore(key)
	cs.mu.Lock()
	started := cs.loadStarted
	cs.mu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for load to start")
	}
}

func (f *blockingLoadStoreFactory) blockClose(key ch.ChannelKey) {
	cs := f.mustStore(key)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.closeBlock = make(chan struct{})
	cs.closeStarted = make(chan struct{}, 1)
}

func (f *blockingLoadStoreFactory) unblockClose(key ch.ChannelKey) {
	cs := f.mustStore(key)
	cs.mu.Lock()
	block := cs.closeBlock
	cs.closeBlock = nil
	cs.mu.Unlock()
	if block != nil {
		close(block)
	}
}

func (f *blockingLoadStoreFactory) waitCloseStarted(t *testing.T, key ch.ChannelKey) {
	t.Helper()
	cs := f.mustStore(key)
	cs.mu.Lock()
	started := cs.closeStarted
	cs.mu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for close to start")
	}
}

func (f *blockingLoadStoreFactory) mustStore(key ch.ChannelKey) *blockingLoadStore {
	f.mu.Lock()
	defer f.mu.Unlock()
	cs := f.stores[key]
	if cs == nil {
		cs = &blockingLoadStore{factory: f, key: key}
		f.stores[key] = cs
	}
	return cs
}

type blockingLoadStore struct {
	factory      *blockingLoadStoreFactory
	key          ch.ChannelKey
	base         store.ChannelStore
	mu           sync.Mutex
	loadBlock    chan struct{}
	loadStarted  chan struct{}
	closeBlock   chan struct{}
	closeStarted chan struct{}
}

func (s *blockingLoadStore) Load(ctx context.Context) (store.InitialState, error) {
	s.mu.Lock()
	block := s.loadBlock
	started := s.loadStarted
	base := s.base
	s.mu.Unlock()
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
	return base.Load(ctx)
}

func (s *blockingLoadStore) AppendLeader(ctx context.Context, req store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	return s.base.AppendLeader(ctx, req)
}

func (s *blockingLoadStore) ApplyFollower(ctx context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	return s.base.ApplyFollower(ctx, req)
}

func (s *blockingLoadStore) ReadCommitted(ctx context.Context, req store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return s.base.ReadCommitted(ctx, req)
}

func (s *blockingLoadStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	return s.base.ReadLog(ctx, req)
}

func (s *blockingLoadStore) StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error {
	return s.base.StoreCheckpoint(ctx, checkpoint)
}

func (s *blockingLoadStore) LoadRetentionState(ctx context.Context) (store.RetentionState, error) {
	return s.base.LoadRetentionState(ctx)
}

func (s *blockingLoadStore) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) (uint64, error) {
	return s.base.AdoptRetentionBoundary(ctx, throughSeq, cursorName)
}

func (s *blockingLoadStore) TrimMessagesThrough(ctx context.Context, throughSeq uint64, opts store.RetentionTrimOptions) (store.RetentionTrimResult, error) {
	return s.base.TrimMessagesThrough(ctx, throughSeq, opts)
}

func (s *blockingLoadStore) LookupMessageByID(ctx context.Context, messageID uint64) (ch.Message, bool, error) {
	lookup, ok := s.base.(store.MessageLookup)
	if !ok {
		return ch.Message{}, false, ch.ErrInvalidConfig
	}
	return lookup.LookupMessageByID(ctx, messageID)
}

func (s *blockingLoadStore) Close() error {
	s.mu.Lock()
	block := s.closeBlock
	started := s.closeStarted
	base := s.base
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block != nil {
		<-block
	}
	if base == nil {
		return nil
	}
	return base.Close()
}
