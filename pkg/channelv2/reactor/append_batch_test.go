package reactor

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/stretchr/testify/require"
)

func TestAppendEventsBatchByMaxRecords(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-batch-records", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 2, AppendBatchMaxWait: time.Hour})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	first, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, first)
	second, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 2, "b"))
	require.NoError(t, err)

	firstResult := awaitFutureResult(t, first)
	secondResult := awaitFutureResult(t, second)
	require.NoError(t, firstResult.Err)
	require.NoError(t, secondResult.Err)
	require.Equal(t, uint64(1), firstResult.AppendBatch.Items[0].MessageSeq)
	require.Equal(t, uint64(2), secondResult.AppendBatch.Items[0].MessageSeq)
	require.Equal(t, []int{2}, factory.appendSizes(meta.Key))
}

func TestAppendEventsBatchByMaxWaitTick(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-batch-wait", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, future)
	require.NoError(t, g.Tick(context.Background()))
	requireFuturePending(t, future)

	tickFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
	require.NoError(t, err)
	_, err = tickFuture.Await(context.Background())
	require.NoError(t, err)
	result := awaitFutureResult(t, future)
	require.NoError(t, result.Err)
	require.Equal(t, []int{1}, factory.appendSizes(meta.Key))
}

func TestMetadataChangeFailsInflightAppendWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	meta := testMeta("append-meta-fences-inflight", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 1})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	factory.waitAppendStarted(t)

	updated := meta
	updated.LeaderEpoch++
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: updated}))
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, ch.ErrStaleMeta)

	factory.unblockAppends()
}

func TestAppendPoolFullKeepsAcceptedRequestPendingAndRetriesOnTick(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	meta := testMeta("append-pool-full-retry", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{
		AppendBatchMaxRecords:   1,
		AppendStoreRetryBackoff: time.Hour,
		WorkerPools:             worker.PoolsConfig{StoreAppend: worker.PoolConfig{Name: "test-store-append", Workers: 1, QueueSize: 1}},
	})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	fillTask := worker.Task{
		Kind:  worker.TaskStoreAppend,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 900},
		StoreAppend: &worker.StoreAppendTask{
			ChannelID: meta.ID,
			Records:   []ch.Record{{ID: 900, Payload: []byte("fill"), SizeBytes: 4}},
			Sync:      true,
		},
	}
	require.NoError(t, g.pools.Submit(context.Background(), fillTask))
	factory.waitAppendStarted(t)
	fillTask.Fence.OpID = 901
	require.NoError(t, g.pools.Submit(context.Background(), fillTask))

	third, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, 3, "c"))
	require.NoError(t, err)
	requireFuturePending(t, third)

	factory.unblockAppends()
	requireFuturePending(t, third)
	tickFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
	require.NoError(t, err)
	_, err = tickFuture.Await(context.Background())
	require.NoError(t, err)
	thirdResult := awaitFutureResult(t, third)
	require.NoError(t, thirdResult.Err)
}

func TestAppendStorePoolBackpressureRollsBackBatchProposalForRetry(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	meta := testMeta("append-pool-backpressure-rollback", 1, 1)
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16,
		AppendBatchMaxRecords: 1, AppendStoreRetryBackoff: time.Millisecond,
		NextOpID: func() ch.OpID { return 100 },
	})
	require.NoError(t, applyMetaDirect(t, r, meta))
	pools, err := worker.NewPools(worker.PoolsConfig{StoreAppend: worker.PoolConfig{Name: "append", Workers: 1, QueueSize: 1}, StoreRead: worker.PoolConfig{Name: "read", Workers: 1, QueueSize: 1}, StoreApply: worker.PoolConfig{Name: "apply", Workers: 1, QueueSize: 1}, RPC: worker.PoolConfig{Name: "rpc", Workers: 1, QueueSize: 1}}, worker.Deps{LocalNode: 1, Stores: factory}, nopCompletionSink{})
	require.NoError(t, err)
	defer pools.Close()
	r.cfg.Pools = pools
	fillTask := worker.Task{
		Kind:  worker.TaskStoreAppend,
		Fence: ch.Fence{ChannelKey: meta.Key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 900},
		StoreAppend: &worker.StoreAppendTask{
			ChannelID: meta.ID,
			Records:   []ch.Record{{ID: 900, Payload: []byte("fill"), SizeBytes: 4}},
			Sync:      true,
		},
	}
	require.NoError(t, pools.Submit(context.Background(), fillTask))
	factory.waitAppendStarted(t)
	fillTask.Fence.OpID = 901
	require.NoError(t, pools.Submit(context.Background(), fillTask))

	future := NewFuture()
	r.handleAppend(appendEventWithFuture(meta, 1, "a", future))
	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Nil(t, rc.state.InflightAppend)
	require.Empty(t, rc.state.PendingAppends)
	require.Len(t, rc.appendQ.pending, 1)
	require.Contains(t, rc.waiters, ch.OpID(1))
	requireFuturePending(t, future)
}

func TestAppendStorePoolBackpressureRollsBackMultiChannelGroupForRetry(t *testing.T) {
	factory := newCountingStoreFactory()
	factory.blockAppends()
	metas := []ch.Meta{
		testMeta("append-pool-backpressure-group-a", 1, 1),
		testMeta("append-pool-backpressure-group-b", 1, 1),
		testMeta("append-pool-backpressure-group-c", 1, 1),
	}
	fillMeta := testMeta("append-pool-backpressure-group-fill", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{
		AppendBatchMaxRecords:   1,
		AppendStoreRetryBackoff: time.Millisecond,
		WorkerPools:             worker.PoolsConfig{StoreAppend: worker.PoolConfig{Name: "test-store-append", Workers: 1, QueueSize: 1}},
	})
	defer g.Close()
	for _, meta := range metas {
		require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	}
	fillTask := worker.Task{
		Kind:  worker.TaskStoreAppend,
		Fence: ch.Fence{ChannelKey: fillMeta.Key, Generation: 1, Epoch: 1, LeaderEpoch: 1, OpID: 900},
		StoreAppend: &worker.StoreAppendTask{
			ChannelID: fillMeta.ID,
			Records:   []ch.Record{{ID: 900, Payload: []byte("fill"), SizeBytes: 4}},
			Sync:      true,
		},
	}
	require.NoError(t, g.pools.Submit(context.Background(), fillTask))
	factory.waitAppendStarted(t)
	fillTask.Fence.OpID = 901
	require.NoError(t, g.pools.Submit(context.Background(), fillTask))

	futures := make([]*Future, 0, len(metas))
	for i, meta := range metas {
		future, err := g.Submit(context.Background(), meta.Key, appendEvent(meta, uint64(i+1), string(rune('a'+i))))
		require.NoError(t, err)
		futures = append(futures, future)
	}
	for i, meta := range metas {
		tickFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
		require.NoError(t, err)
		_, err = tickFuture.Await(context.Background())
		require.NoError(t, err)
		requireFuturePending(t, futures[i])
	}

	factory.unblockAppends()
	factory.waitAppendStarted(t)
	for _, meta := range metas {
		tickFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventTick, Key: meta.Key, TickNow: time.Now().Add(2 * time.Hour)})
		require.NoError(t, err)
		_, err = tickFuture.Await(context.Background())
		require.NoError(t, err)
	}
	for i, meta := range metas {
		result := awaitFutureResult(t, futures[i])
		require.NoError(t, result.Err)
		require.Equal(t, uint64(1), result.AppendBatch.Items[0].MessageSeq)
		require.Equal(t, []int{1}, factory.appendSizes(meta.Key))
	}
}

func TestAppendContextCancelRemovesAcceptedWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-cancel-removes", 1, 1)
	g := newAppendBatchTestGroup(t, factory, Config{AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour})
	defer g.Close()
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	ctx, cancel := context.WithCancel(context.Background())
	future, err := g.Submit(ctx, meta.Key, appendEvent(meta, 1, "a"))
	require.NoError(t, err)
	requireFuturePending(t, future)
	cancel()
	cleanup, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventCancelWaiter, Key: meta.Key, CancelOp: 1, CancelErr: context.Canceled})
	require.NoError(t, err)
	_, err = cleanup.Await(context.Background())
	require.NoError(t, err)
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, context.Canceled)
}

func TestAppendContextCancelRemovesPostStoreQuorumWaiter(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := ch.Meta{
		Key:         ch.ChannelKey("1:append-cancel-post-store"),
		ID:          ch.ChannelID{ID: "append-cancel-post-store", Type: 1},
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []ch.NodeID{1, 2},
		ISR:         []ch.NodeID{1, 2},
		MinISR:      2,
		Status:      ch.StatusActive,
	}
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16, AppendBatchMaxRecords: 1})
	require.NoError(t, applyMetaDirect(t, r, meta))

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	future := NewFuture()
	require.NoError(t, rc.addWaiter(1, future))
	decision := rc.state.ProposeAppendBatch(machine.AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []machine.AppendBatchWaiter{{
			OpID:       1,
			CommitMode: ch.CommitModeQuorum,
			Records:    []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}},
		}},
	})
	require.NoError(t, decision.Err)
	require.Len(t, decision.Tasks, 1)
	task := decision.Tasks[0]
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	appendResult, err := cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: task.StoreAppend.Records, Sync: true})
	require.NoError(t, err)
	r.handleStoreAppendResult(worker.Result{
		Kind:        worker.TaskStoreAppend,
		Fence:       task.Fence,
		StoreAppend: &worker.StoreAppendResult{BaseOffset: appendResult.BaseOffset, LastOffset: appendResult.LastOffset},
	})
	require.Contains(t, rc.state.PendingAppends, ch.OpID(1))
	require.Equal(t, []ch.OpID{1}, rc.state.PendingAppendOrder)
	requireFuturePending(t, future)

	r.handleCancelWaiter(Event{Kind: EventCancelWaiter, Key: meta.Key, CancelOp: 1, CancelErr: context.Canceled, Future: NewFuture()})
	_, err = future.Await(context.Background())
	require.ErrorIs(t, err, context.Canceled)

	require.NotContains(t, rc.state.PendingAppends, ch.OpID(1))
	require.NotContains(t, rc.state.PendingAppendOrder, ch.OpID(1))
	logResult, err := cs.ReadLog(context.Background(), store.ReadLogRequest{FromOffset: 1, MaxOffset: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, logResult.Records, 1)
}

func TestAppendContextCanceledBeforeAdmissionRejectsWithoutQueueing(t *testing.T) {
	factory := newCountingStoreFactory()
	meta := testMeta("append-cancel-before-admission", 1, 1)
	r := NewReactor(ReactorConfig{
		ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16,
		AppendBatchMaxRecords: 10, AppendBatchMaxWait: time.Hour,
	})
	require.NoError(t, applyMetaDirect(t, r, meta))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	future := NewFuture()
	event := appendEventWithFuture(meta, 1, "a", future)
	event.Context = ctx

	r.handleAppend(event)

	awaitCtx, awaitCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer awaitCancel()
	_, err := future.Await(awaitCtx)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, factory.appendSizes(meta.Key))

	rc := r.channels[meta.Key]
	require.NotNil(t, rc)
	require.Empty(t, rc.appendQ.pending)
	require.Empty(t, rc.waiters)
	require.Empty(t, rc.state.PendingAppends)
	require.Nil(t, rc.state.InflightAppend)
}

func appendEventWithFuture(meta ch.Meta, id uint64, payload string, future *Future) Event {
	event := appendEvent(meta, id, payload)
	event.Future = future
	return event
}

func newAppendBatchTestGroup(t *testing.T, factory store.Factory, cfg Config) *Group {
	t.Helper()
	cfg.LocalNode = 1
	cfg.ReactorCount = 1
	cfg.MailboxSize = 16
	cfg.Store = factory
	g, err := NewGroup(cfg)
	require.NoError(t, err)
	return g
}

type countingStoreFactory struct {
	base    *store.MemoryFactory
	mu      sync.Mutex
	stores  map[ch.ChannelKey]*countingStore
	blockCh chan struct{}
	started chan struct{}
}

func newCountingStoreFactory() *countingStoreFactory {
	return &countingStoreFactory{base: store.NewMemoryFactory(), stores: make(map[ch.ChannelKey]*countingStore)}
}

func (f *countingStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing := f.stores[key]; existing != nil {
		return existing, nil
	}
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	cs := &countingStore{factory: f, key: key, base: base}
	f.stores[key] = cs
	return cs, nil
}

func (f *countingStoreFactory) appendSizes(key ch.ChannelKey) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stores[key] == nil {
		return nil
	}
	return append([]int(nil), f.stores[key].appendSizes...)
}

func (f *countingStoreFactory) blockAppends() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockCh = make(chan struct{})
	f.started = make(chan struct{}, 16)
}

func (f *countingStoreFactory) unblockAppends() {
	f.mu.Lock()
	blockCh := f.blockCh
	f.blockCh = nil
	f.mu.Unlock()
	if blockCh != nil {
		close(blockCh)
	}
}

func (f *countingStoreFactory) waitAppendStarted(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	started := f.started
	f.mu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for append to start")
	}
}

type countingStore struct {
	factory     *countingStoreFactory
	key         ch.ChannelKey
	base        store.ChannelStore
	appendSizes []int
}

func (s *countingStore) Load(ctx context.Context) (store.InitialState, error) {
	return s.base.Load(ctx)
}
func (s *countingStore) ApplyFollower(ctx context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	return s.base.ApplyFollower(ctx, req)
}
func (s *countingStore) ReadCommitted(ctx context.Context, req store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return s.base.ReadCommitted(ctx, req)
}
func (s *countingStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	return s.base.ReadLog(ctx, req)
}
func (s *countingStore) StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error {
	return s.base.StoreCheckpoint(ctx, checkpoint)
}
func (s *countingStore) Close() error { return s.base.Close() }

func (s *countingStore) AppendLeader(ctx context.Context, req store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	s.factory.mu.Lock()
	s.appendSizes = append(s.appendSizes, len(req.Records))
	blockCh := s.factory.blockCh
	started := s.factory.started
	s.factory.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if blockCh != nil {
		select {
		case <-blockCh:
		case <-ctx.Done():
			return store.AppendLeaderResult{}, ctx.Err()
		}
	}
	return s.base.AppendLeader(ctx, req)
}

type nopCompletionSink struct{}

func (nopCompletionSink) Complete(worker.Result) {}
