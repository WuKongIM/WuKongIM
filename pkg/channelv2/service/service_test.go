package service

import (
	"context"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
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
			require.NoError(t, err)
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
	base  *store.MemoryFactory
	mu    sync.Mutex
	calls map[ch.ChannelKey]int
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

type serviceCountingStore struct {
	factory *serviceCountingStoreFactory
	key     ch.ChannelKey
	base    store.ChannelStore
}

func (s *serviceCountingStore) Load(ctx context.Context) (store.InitialState, error) {
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
