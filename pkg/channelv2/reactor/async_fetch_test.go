package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestFetchSubmitsStoreReadWithoutBlockingReactor(t *testing.T) {
	factory := newBlockingReadFactory()
	meta := testMeta("async-nonblocking", 1, 1)
	seedCommittedMessage(t, factory, meta, 1, "hello")
	g := newAsyncFetchTestGroup(t, factory, worker.PoolConfig{})
	defer g.Close()
	defer factory.unblockAll()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	fetchFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 7, Fetch: fetchRequest(meta)})
	require.NoError(t, err)
	factory.waitStarted(t)

	applyCtx, cancelApply := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelApply()
	applyFuture, err := g.Submit(applyCtx, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	_, err = applyFuture.Await(applyCtx)
	require.NoError(t, err)

	factory.unblockAll()
	fetchResult := awaitFutureResult(t, fetchFuture)
	require.NoError(t, fetchResult.Err)
	require.Len(t, fetchResult.Fetch.Messages, 1)
	require.Equal(t, uint64(1), fetchResult.Fetch.Messages[0].MessageSeq)
	require.Equal(t, uint64(1), fetchResult.Fetch.CommittedSeq)
}

func TestFetchStoreReadPoolFullFailsFuture(t *testing.T) {
	factory := newBlockingReadFactory()
	meta := testMeta("async-backpressure", 1, 1)
	seedCommittedMessage(t, factory, meta, 1, "hello")
	g := newAsyncFetchTestGroup(t, factory, worker.PoolConfig{Name: "test-store-read", Workers: 1, QueueSize: 1})
	defer g.Close()
	defer factory.unblockAll()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	first, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 1, Fetch: fetchRequest(meta)})
	require.NoError(t, err)
	factory.waitStarted(t)
	second, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 2, Fetch: fetchRequest(meta)})
	require.NoError(t, err)
	third, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 3, Fetch: fetchRequest(meta)})
	require.NoError(t, err)

	thirdCtx, cancelThird := context.WithTimeout(context.Background(), time.Second)
	defer cancelThird()
	_, err = third.Await(thirdCtx)
	require.ErrorIs(t, err, ch.ErrBackpressured)

	factory.unblockAll()
	firstResult := awaitFutureResult(t, first)
	require.NoError(t, firstResult.Err)
	require.Len(t, firstResult.Fetch.Messages, 1)
	secondResult := awaitFutureResult(t, second)
	require.NoError(t, secondResult.Err)
	require.Len(t, secondResult.Fetch.Messages, 1)
}

func TestFetchMetadataChangeFailsPendingWaiter(t *testing.T) {
	factory := newBlockingReadFactory()
	meta := testMeta("async-stale-meta", 1, 1)
	seedCommittedMessage(t, factory, meta, 1, "hello")
	g := newAsyncFetchTestGroup(t, factory, worker.PoolConfig{})
	defer g.Close()
	defer factory.unblockAll()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	fetchFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 4, Fetch: fetchRequest(meta)})
	require.NoError(t, err)
	factory.waitStarted(t)

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: updated}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = fetchFuture.Await(ctx)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	factory.unblockAll()
}

func TestFetchGroupCloseCancelsBlockedReadAndFailsFuture(t *testing.T) {
	factory := newBlockingReadFactory()
	meta := testMeta("async-close-blocked-fetch", 1, 1)
	seedCommittedMessage(t, factory, meta, 1, "hello")
	g := newAsyncFetchTestGroup(t, factory, worker.PoolConfig{Name: "test-store-read", Workers: 1, QueueSize: 1})
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	fetchFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 6, Fetch: fetchRequest(meta)})
	require.NoError(t, err)
	factory.waitStarted(t)

	closed := make(chan error, 1)
	go func() { closed <- g.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		factory.unblockAll()
		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("group Close stayed blocked after releasing ReadCommitted")
		}
		t.Fatal("group Close did not cancel blocked async fetch")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = fetchFuture.Await(ctx)
	require.ErrorIs(t, err, ch.ErrClosed)
}

func TestFetchApplyMetaFailsPendingWaiterBeforeStateMutation(t *testing.T) {
	meta := testMeta("async-stale-meta-order", 1, 1)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)

	hookEntered := make(chan Result, 1)
	releaseHook := make(chan struct{})
	fetchFuture := NewFuture()
	fetchFuture.beforeComplete = func(result Result) {
		hookEntered <- result
		<-releaseHook
	}
	rc.addFetchWaiter(44, fetchFuture)
	updated := meta
	updated.LeaderEpoch++

	done := make(chan struct{})
	applyFuture := NewFuture()
	go func() {
		r.handleApplyMeta(Event{Kind: EventApplyMeta, Key: meta.Key, Meta: updated, Future: applyFuture})
		close(done)
	}()

	var staleResult Result
	select {
	case staleResult = <-hookEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pending fetch completion hook")
	}
	require.ErrorIs(t, staleResult.Err, ch.ErrStaleMeta)
	require.Equal(t, meta.LeaderEpoch, rc.state.LeaderEpoch)
	require.Equal(t, meta.Epoch, rc.state.Epoch)
	require.Equal(t, meta.Status, rc.state.Status)
	close(releaseHook)

	_, err := fetchFuture.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	result := awaitFutureResult(t, applyFuture)
	require.NoError(t, result.Err)
	require.Equal(t, updated.LeaderEpoch, rc.state.LeaderEpoch)
}

func TestFetchApplyMetaRejectedChangeKeepsPendingWaiter(t *testing.T) {
	meta := testMeta("async-stale-meta-rejected", 1, 1)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)

	fetchFuture := NewFuture()
	rc.addFetchWaiter(45, fetchFuture)
	rejected := meta
	rejected.LeaderEpoch++
	rejected.MinISR = len(rejected.ISR) + 1

	err := applyMetaDirect(t, r, rejected)
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Equal(t, meta.LeaderEpoch, rc.state.LeaderEpoch)
	requireFuturePending(t, fetchFuture)
	require.Contains(t, rc.fetchWaiters, ch.OpID(45))
}

func TestAppendMetadataChangeFailsPendingQuorumWaiter(t *testing.T) {
	meta := testMeta("append-stale-meta", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2, 3}
	meta.MinISR = 2
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))

	appendFuture := NewFuture()
	r.handleAppend(Event{
		Kind:   EventAppend,
		Key:    meta.Key,
		OpID:   77,
		Future: appendFuture,
		Append: ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeQuorum,
			Messages: []ch.Message{{
				MessageID:   77,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("pending"),
			}},
		},
	})

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Contains(t, rc.waiters, ch.OpID(77))
	require.NotContains(t, rc.fetchWaiters, ch.OpID(77))
	requireFuturePending(t, appendFuture)

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, applyMetaDirect(t, r, updated))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := appendFuture.Await(ctx)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Empty(t, rc.waiters)
	require.Empty(t, rc.fetchWaiters)
	require.Empty(t, rc.state.PendingAppends)
	require.Nil(t, rc.state.InflightAppend)
}

func TestAppendMetadataRejectedChangeKeepsPendingQuorumWaiter(t *testing.T) {
	meta := testMeta("append-stale-meta-rejected", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2, 3}
	meta.ISR = []ch.NodeID{1, 2, 3}
	meta.MinISR = 2
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))

	appendFuture := NewFuture()
	r.handleAppend(Event{
		Kind:   EventAppend,
		Key:    meta.Key,
		OpID:   78,
		Future: appendFuture,
		Append: ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeQuorum,
			Messages: []ch.Message{{
				MessageID:   78,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("pending"),
			}},
		},
	})

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Contains(t, rc.waiters, ch.OpID(78))

	rejected := meta
	rejected.LeaderEpoch++
	rejected.MinISR = len(rejected.ISR) + 1
	err := applyMetaDirect(t, r, rejected)

	require.ErrorIs(t, err, ch.ErrInvalidConfig)
	require.Equal(t, meta.LeaderEpoch, rc.state.LeaderEpoch)
	require.Contains(t, rc.waiters, ch.OpID(78))
	require.NotContains(t, rc.fetchWaiters, ch.OpID(78))
	requireFuturePending(t, appendFuture)
}

func TestFetchStoreReadErrorCompletesFuture(t *testing.T) {
	factory := newReadErrorFactory(ch.ErrNotReady)
	meta := testMeta("async-read-error", 1, 1)
	seedCommittedMessage(t, factory, meta, 1, "hello")
	g := newAsyncFetchTestGroup(t, factory, worker.PoolConfig{})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	fetchFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventFetch, Key: meta.Key, OpID: 5, Fetch: fetchRequest(meta)})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = fetchFuture.Await(ctx)
	require.ErrorIs(t, err, ch.ErrNotReady)
}

func newAsyncFetchTestGroup(t *testing.T, factory store.Factory, storeRead worker.PoolConfig) *Group {
	t.Helper()
	g, err := NewGroup(Config{
		LocalNode:    1,
		ReactorCount: 1,
		MailboxSize:  16,
		Store:        factory,
		WorkerPools:  worker.PoolsConfig{StoreRead: storeRead},
	})
	require.NoError(t, err)
	return g
}

func fetchRequest(meta ch.Meta) ch.FetchRequest {
	return ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024}
}

func applyMetaDirect(t *testing.T, reactor *Reactor, meta ch.Meta) error {
	t.Helper()
	future := NewFuture()
	reactor.handleApplyMeta(Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta, Future: future})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := future.Await(ctx)
	return err
}

func seedCommittedMessage(t *testing.T, factory store.Factory, meta ch.Meta, id uint64, payload string) {
	t.Helper()
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: []ch.Record{{ID: id, Payload: []byte(payload), SizeBytes: len(payload)}}})
	require.NoError(t, err)
	require.NoError(t, cs.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 1}))
}

func awaitFutureResult(t *testing.T, future *Future) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := future.Await(ctx)
	require.NoError(t, err)
	return result
}

type blockingReadFactory struct {
	inner   *store.MemoryFactory
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
}

func newBlockingReadFactory() *blockingReadFactory {
	return &blockingReadFactory{inner: store.NewMemoryFactory(), started: make(chan struct{}, 16), unblock: make(chan struct{})}
}

func (f *blockingReadFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	cs, err := f.inner.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingReadStore{ChannelStore: cs, factory: f}, nil
}

func (f *blockingReadFactory) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReadCommitted to start")
	}
}

func (f *blockingReadFactory) unblockAll() {
	f.once.Do(func() { close(f.unblock) })
}

type blockingReadStore struct {
	store.ChannelStore
	factory *blockingReadFactory
}

func (s *blockingReadStore) ReadCommitted(ctx context.Context, req store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	s.factory.started <- struct{}{}
	select {
	case <-s.factory.unblock:
	case <-ctx.Done():
		return store.ReadCommittedResult{}, ctx.Err()
	}
	return s.ChannelStore.ReadCommitted(ctx, req)
}

type readErrorFactory struct {
	inner *store.MemoryFactory
	err   error
}

func newReadErrorFactory(err error) *readErrorFactory {
	return &readErrorFactory{inner: store.NewMemoryFactory(), err: err}
}

func (f *readErrorFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	cs, err := f.inner.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &readErrorStore{ChannelStore: cs, err: f.err}, nil
}

type readErrorStore struct {
	store.ChannelStore
	err error
}

func (s *readErrorStore) ReadCommitted(ctx context.Context, req store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return store.ReadCommittedResult{}, s.err
}
