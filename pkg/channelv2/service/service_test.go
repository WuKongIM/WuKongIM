package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/stretchr/testify/require"
)

func TestSingleNodeAppendFetchCommitted(t *testing.T) {
	factory := store.NewMemoryFactory()
	cluster, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1})
	require.NoError(t, err)
	defer cluster.Close()

	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, cluster.ApplyMeta(meta))

	appendRes, err := cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), appendRes.MessageSeq)

	fetchRes, err := cluster.Fetch(context.Background(), ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, fetchRes.Messages, 1)
	require.Equal(t, uint64(1), fetchRes.CommittedSeq)
}

func TestAppendLazyLoadsMetaBeforeSubmittingToReactor(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := ch.Meta{Key: ch.ChannelKey("1:lazy"), ID: ch.ChannelID{ID: "lazy", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	resolver := &countingMetaResolver{meta: meta}
	cluster, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer cluster.Close()

	appendRes, err := cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("hello")}})
	require.NoError(t, err)
	require.Equal(t, uint64(1), appendRes.MessageSeq)
	require.Equal(t, int32(1), resolver.calls.Load())
}

func TestAppendUsesExistingReactorStateWithoutResolvingMetaAgain(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := ch.Meta{Key: ch.ChannelKey("1:cached"), ID: ch.ChannelID{ID: "cached", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	resolver := &countingMetaResolver{meta: meta}
	cluster, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1, MetaResolver: resolver})
	require.NoError(t, err)
	defer cluster.Close()

	_, err = cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("first")}})
	require.NoError(t, err)
	_, err = cluster.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{Payload: []byte("second")}})
	require.NoError(t, err)
	require.Equal(t, int32(1), resolver.calls.Load())
}

func TestHandlePullUsesAllocatedOpIDAndReturnsRecords(t *testing.T) {
	factory := newServiceBlockingReadLogFactory()
	clusterAPI, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1})
	require.NoError(t, err)
	defer clusterAPI.Close()
	svc := clusterAPI.(*cluster)

	meta := ch.Meta{Key: ch.ChannelKey("1:a"), ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, clusterAPI.ApplyMeta(meta))
	_, err = clusterAPI.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: 10, Payload: []byte("a")}})
	require.NoError(t, err)

	pullCh := make(chan transport.PullResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		pull, err := svc.HandlePull(context.Background(), transport.PullRequest{ChannelKey: meta.Key, ChannelID: meta.ID, Epoch: 1, LeaderEpoch: 1, Follower: 2, NextOffset: 1, MaxBytes: 1024})
		pullCh <- pull
		errCh <- err
	}()
	factory.waitReadLogStarted(t)

	metaFuture, err := svc.group.Submit(context.Background(), meta.Key, reactor.Event{Kind: reactor.EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	metaCtx, metaCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer metaCancel()
	_, err = metaFuture.Await(metaCtx)
	require.NoError(t, err)

	factory.UnblockReadLogs()
	select {
	case err := <-errCh:
		require.NoError(t, err)
		pull := <-pullCh
		require.Len(t, pull.Records, 1)
		require.Equal(t, uint64(1), pull.Records[0].Index)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pull response")
	}
}

func TestAppendBatchContextCancelAfterAdmissionCleansQueuedWaiter(t *testing.T) {
	factory := newServiceCountingStoreFactory()
	clusterAPI, err := New(Config{
		LocalNode:             1,
		Store:                 factory,
		ReactorCount:          1,
		AppendBatchMaxRecords: 10,
		AppendBatchMaxWait:    time.Hour,
	})
	require.NoError(t, err)
	cluster := clusterAPI.(*cluster)
	defer cluster.Close()

	meta := ch.Meta{Key: ch.ChannelKey("1:cancel-after-admission"), ID: ch.ChannelID{ID: "cancel-after-admission", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, cluster.ApplyMeta(meta))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := cluster.AppendBatch(ctx, ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeLocal,
			Messages: []ch.Message{{
				MessageID:   1,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("cancel-me"),
			}},
		})
		errCh <- err
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			t.Fatalf("append returned before cancellation: %v", err)
			return false
		default:
		}
		return awaitServiceTick(t, cluster, meta.Key, time.Now()) && factory.appendCalls(meta.Key) == 0
	}, time.Second, time.Millisecond)

	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled append")
	}

	require.True(t, awaitServiceTick(t, cluster, meta.Key, time.Now().Add(2*time.Hour)))
	require.Equal(t, 0, factory.appendCalls(meta.Key))
}

func TestAppendBatchContextCancelReturnsContextErrorWhenCleanupBackpressured(t *testing.T) {
	factory := newServiceCountingStoreFactory()
	clusterAPI, err := New(Config{
		LocalNode:             1,
		Store:                 factory,
		ReactorCount:          1,
		MailboxSize:           1,
		AppendBatchMaxRecords: 10,
		AppendBatchMaxWait:    time.Hour,
	})
	require.NoError(t, err)
	cluster := clusterAPI.(*cluster)
	defer cluster.Close()

	meta := ch.Meta{Key: ch.ChannelKey("1:cancel-cleanup-backpressured"), ID: ch.ChannelID{ID: "cancel-cleanup-backpressured", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, cluster.ApplyMeta(meta))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := cluster.AppendBatch(ctx, ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeLocal,
			Messages: []ch.Message{{
				MessageID:   1,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte("cancel-me"),
			}},
		})
		errCh <- err
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			t.Fatalf("append returned before cancellation: %v", err)
			return false
		default:
		}
		return awaitServiceTick(t, cluster, meta.Key, time.Now()) && factory.appendCalls(meta.Key) == 0
	}, time.Second, time.Millisecond)

	blockMeta := ch.Meta{Key: ch.ChannelKey("1:block-cleanup"), ID: ch.ChannelID{ID: "block-cleanup", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	factory.blockLoad(blockMeta.Key)
	defer factory.unblockLoad()
	blockFuture, err := cluster.group.Submit(context.Background(), blockMeta.Key, reactor.Event{Kind: reactor.EventApplyMeta, Key: blockMeta.Key, Meta: blockMeta})
	require.NoError(t, err)
	factory.waitLoadStarted(t)

	fillerMeta := ch.Meta{Key: ch.ChannelKey("1:fill-cleanup"), ID: ch.ChannelID{ID: "fill-cleanup", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	fillerFuture, err := cluster.group.Submit(context.Background(), fillerMeta.Key, reactor.Event{Kind: reactor.EventApplyMeta, Key: fillerMeta.Key, Meta: fillerMeta})
	require.NoError(t, err)

	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, ch.ErrBackpressured)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled append")
	}

	factory.unblockLoad()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	_, err = blockFuture.Await(waitCtx)
	require.NoError(t, err)
	_, err = fillerFuture.Await(waitCtx)
	require.NoError(t, err)
}

type countingMetaResolver struct {
	meta  ch.Meta
	calls atomic.Int32
}

func (r *countingMetaResolver) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	if id != r.meta.ID {
		return ch.Meta{}, ch.ErrChannelNotFound
	}
	r.calls.Add(1)
	return r.meta, nil
}

func awaitServiceTick(t *testing.T, cluster *cluster, key ch.ChannelKey, now time.Time) bool {
	t.Helper()
	future, err := cluster.group.Submit(context.Background(), key, reactor.Event{Kind: reactor.EventTick, Key: key, TickNow: now})
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = future.Await(ctx)
	return err == nil
}

type serviceCountingStoreFactory struct {
	base        *store.MemoryFactory
	mu          sync.Mutex
	calls       map[ch.ChannelKey]int
	blockLoadOn ch.ChannelKey
	loadStarted chan struct{}
	unblock     chan struct{}
}

func newServiceCountingStoreFactory() *serviceCountingStoreFactory {
	return &serviceCountingStoreFactory{base: store.NewMemoryFactory(), calls: make(map[ch.ChannelKey]int)}
}

func (f *serviceCountingStoreFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &serviceCountingStore{factory: f, key: key, base: base}, nil
}

func (f *serviceCountingStoreFactory) appendCalls(key ch.ChannelKey) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *serviceCountingStoreFactory) blockLoad(key ch.ChannelKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockLoadOn = key
	f.loadStarted = make(chan struct{}, 1)
	f.unblock = make(chan struct{})
}

func (f *serviceCountingStoreFactory) waitLoadStarted(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	started := f.loadStarted
	f.mu.Unlock()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for load to block")
	}
}

func (f *serviceCountingStoreFactory) unblockLoad() {
	f.mu.Lock()
	unblock := f.unblock
	f.unblock = nil
	f.blockLoadOn = ""
	f.mu.Unlock()
	if unblock != nil {
		close(unblock)
	}
}

type serviceCountingStore struct {
	factory *serviceCountingStoreFactory
	key     ch.ChannelKey
	base    store.ChannelStore
}

func (s *serviceCountingStore) Load(ctx context.Context) (store.InitialState, error) {
	s.factory.mu.Lock()
	blocked := s.factory.blockLoadOn == s.key
	started := s.factory.loadStarted
	unblock := s.factory.unblock
	s.factory.mu.Unlock()
	if blocked {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-unblock:
		case <-ctx.Done():
			return store.InitialState{}, ctx.Err()
		}
	}
	return s.base.Load(ctx)
}

func (s *serviceCountingStore) AppendLeader(ctx context.Context, req store.AppendLeaderRequest) (store.AppendLeaderResult, error) {
	s.factory.mu.Lock()
	s.factory.calls[s.key]++
	s.factory.mu.Unlock()
	return s.base.AppendLeader(ctx, req)
}

func (s *serviceCountingStore) ApplyFollower(ctx context.Context, req store.ApplyFollowerRequest) (store.ApplyFollowerResult, error) {
	return s.base.ApplyFollower(ctx, req)
}

func (s *serviceCountingStore) ReadCommitted(ctx context.Context, req store.ReadCommittedRequest) (store.ReadCommittedResult, error) {
	return s.base.ReadCommitted(ctx, req)
}

func (s *serviceCountingStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	return s.base.ReadLog(ctx, req)
}

func (s *serviceCountingStore) StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error {
	return s.base.StoreCheckpoint(ctx, checkpoint)
}

func (s *serviceCountingStore) Close() error {
	return s.base.Close()
}

type serviceBlockingReadLogFactory struct {
	base    *store.MemoryFactory
	started chan struct{}
	unblock chan struct{}
}

func newServiceBlockingReadLogFactory() *serviceBlockingReadLogFactory {
	return &serviceBlockingReadLogFactory{base: store.NewMemoryFactory(), started: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (f *serviceBlockingReadLogFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &serviceBlockingReadLogStore{ChannelStore: base, parent: f}, nil
}

func (f *serviceBlockingReadLogFactory) waitReadLogStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReadLog to start")
	}
}

func (f *serviceBlockingReadLogFactory) UnblockReadLogs() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type serviceBlockingReadLogStore struct {
	store.ChannelStore
	parent *serviceBlockingReadLogFactory
}

func (s *serviceBlockingReadLogStore) ReadLog(ctx context.Context, req store.ReadLogRequest) (store.ReadLogResult, error) {
	select {
	case s.parent.started <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return store.ReadLogResult{}, ctx.Err()
	}
	return s.ChannelStore.ReadLog(ctx, req)
}
