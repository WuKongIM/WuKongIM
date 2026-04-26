package replica

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

type blockingApplyDurableStore struct {
	spyDurableStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockingApplyDurableStore) ApplyFollowerBatch(ctx context.Context, req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (uint64, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return s.spyDurableStore.ApplyFollowerBatch(ctx, req, epochPoint)
}

func (s *blockingApplyDurableStore) TruncateLogAndHistory(ctx context.Context, to uint64) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.spyDurableStore.TruncateLogAndHistory(ctx, to)
}

func TestApplyFetchAdvancesCheckpointToMinLeaderHWAndLEO(t *testing.T) {
	env := newFollowerEnv(t)
	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      7,
		Leader:     1,
		Records:    []channel.Record{{Index: 1, Payload: []byte("a"), SizeBytes: 1}},
		LeaderHW:   10,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), env.checkpoints.lastStored().HW)
}

func TestApplyFetchUsesDurableAdapterForEpochBoundary(t *testing.T) {
	env := newFollowerEnv(t)
	meta := activeMeta(8, 1)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeFollower(meta))

	spy := &spyDurableStore{applyLEO: 1}
	env.replica.durable = spy

	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      8,
		Leader:     1,
		Records:    []channel.Record{{Index: 1, Payload: []byte("a"), SizeBytes: 1}},
		LeaderHW:   1,
	})

	require.NoError(t, err)
	require.Equal(t, 1, spy.applyCalls)
	require.Equal(t, channel.EpochPoint{Epoch: 8, StartOffset: 0}, *spy.applyEpochPoint)
	require.Equal(t, channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 1}, *spy.applyReq.Checkpoint)
	require.Equal(t, uint64(1), env.replica.state.LEO)
	require.Equal(t, uint64(1), env.replica.state.HW)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, {Epoch: 8, StartOffset: 0}}, env.replica.epochHistory)
	require.Equal(t, uint64(0), env.log.LEO(), "durable adapter owns record persistence")
}

func TestApplyFetchStoresHeartbeatCheckpointThroughDurableAdapter(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 5
	env.replica.state.LEO = 5
	env.replica.state.HW = 2
	env.replica.state.CheckpointHW = 2
	env.replica.publishStateLocked()

	spy := &spyDurableStore{}
	env.replica.durable = spy

	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      7,
		Leader:     1,
		LeaderHW:   4,
	})

	require.NoError(t, err)
	require.Equal(t, 1, spy.checkpointCalls)
	require.Equal(t, channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 4}, spy.storedCheckpoint)
	require.Equal(t, uint64(4), spy.visibleHW)
	require.Equal(t, uint64(5), spy.checkpointLEO)
	require.Empty(t, env.checkpoints.stored, "durable adapter owns checkpoint persistence")
	require.Equal(t, uint64(4), env.replica.state.HW)
	require.Equal(t, uint64(4), env.replica.state.CheckpointHW)
}

func TestApplyFetchStaleResultAfterMetaChangeIsFenced(t *testing.T) {
	env := newFollowerEnv(t)
	blocking := &blockingApplyDurableStore{
		spyDurableStore: spyDurableStore{applyLEO: 1},
		entered:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}
	env.replica.durable = blocking

	applied := make(chan error, 1)
	go func() {
		applied <- env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
			ChannelKey: "group-10",
			Epoch:      7,
			Leader:     1,
			Records:    []channel.Record{{Index: 1, Payload: []byte("a"), SizeBytes: 1}},
			LeaderHW:   1,
		})
	}()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("durable apply did not start")
	}

	metaApplied := make(chan error, 1)
	go func() {
		metaApplied <- env.replica.ApplyMeta(activeMetaWithMinISR(8, 1, 1))
	}()
	select {
	case err := <-metaApplied:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		close(blocking.release)
		t.Fatal("meta change was blocked by durable apply")
	}
	close(blocking.release)

	select {
	case err := <-applied:
		require.ErrorIs(t, err, channel.ErrStaleMeta)
	case <-time.After(time.Second):
		t.Fatal("apply fetch did not return after durable release")
	}
	require.Equal(t, uint64(8), env.replica.Status().Epoch)
	require.Equal(t, uint64(0), env.replica.Status().LEO)
}

func TestApplyFetchTruncateResultAfterMetaChangeIsFenced(t *testing.T) {
	env := newFollowerEnv(t)
	env.replica.state.LEO = 4
	env.replica.state.HW = 2
	env.replica.state.CheckpointHW = 2
	env.replica.publishStateLocked()
	truncateTo := uint64(2)
	blocking := &blockingApplyDurableStore{
		spyDurableStore: spyDurableStore{},
		entered:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}
	env.replica.durable = blocking

	applied := make(chan error, 1)
	go func() {
		applied <- env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
			ChannelKey: "group-10",
			Epoch:      7,
			Leader:     1,
			TruncateTo: &truncateTo,
			LeaderHW:   2,
		})
	}()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("durable truncate did not start")
	}

	metaApplied := make(chan error, 1)
	go func() {
		metaApplied <- env.replica.ApplyMeta(activeMetaWithMinISR(8, 1, 1))
	}()
	select {
	case err := <-metaApplied:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		close(blocking.release)
		t.Fatal("meta change was blocked by durable truncate")
	}
	close(blocking.release)

	select {
	case err := <-applied:
		require.ErrorIs(t, err, channel.ErrStaleMeta)
	case <-time.After(time.Second):
		t.Fatal("truncate apply did not return after durable release")
	}
	st := env.replica.Status()
	require.Equal(t, uint64(8), st.Epoch)
	require.Equal(t, uint64(2), st.LEO)
}

func TestApplyFetchTruncateResultAfterTombstoneIsDiscarded(t *testing.T) {
	env := newFollowerEnv(t)
	env.replica.state.LEO = 4
	env.replica.state.HW = 2
	env.replica.state.CheckpointHW = 2
	env.replica.publishStateLocked()
	truncateTo := uint64(2)
	blocking := &blockingApplyDurableStore{
		spyDurableStore: spyDurableStore{},
		entered:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}
	env.replica.durable = blocking

	applied := make(chan error, 1)
	go func() {
		applied <- env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
			ChannelKey: "group-10",
			Epoch:      7,
			Leader:     1,
			TruncateTo: &truncateTo,
			LeaderHW:   2,
		})
	}()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("durable truncate did not start")
	}

	require.NoError(t, env.replica.Tombstone())
	close(blocking.release)

	select {
	case err := <-applied:
		require.ErrorIs(t, err, channel.ErrTombstoned)
	case <-time.After(time.Second):
		t.Fatal("truncate apply did not return after durable release")
	}
	st := env.replica.Status()
	require.Equal(t, channel.ReplicaRoleTombstoned, st.Role)
	require.Equal(t, uint64(4), st.LEO)
}

func TestApplyFetchRejectsNonContiguousRecordIndex(t *testing.T) {
	env := newFollowerEnv(t)
	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      7,
		Leader:     1,
		Records:    []channel.Record{{Index: 2, Payload: []byte("a"), SizeBytes: 1}},
		LeaderHW:   2,
	})
	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Zero(t, env.log.LEO())
}

func TestApplyFetchRejectsStaleEpoch(t *testing.T) {
	env := newFollowerEnv(t)
	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      6,
		Leader:     1,
		LeaderHW:   0,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)
}

func TestApplyFetchRejectsTruncateBelowHW(t *testing.T) {
	env := newFollowerEnv(t)
	env.replica.state.HW = 4
	env.replica.publishStateLocked()
	truncateTo := uint64(3)
	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      7,
		Leader:     1,
		TruncateTo: &truncateTo,
		LeaderHW:   4,
	})
	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestApplyFetchIgnoresEmptyRegressiveLeaderHW(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 7

	env.replica.state.HW = 5
	env.replica.state.LEO = 7
	env.replica.state.CheckpointHW = 5
	env.replica.state.CommitReady = true
	env.replica.publishStateLocked()

	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      7,
		Leader:     1,
		LeaderHW:   4,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(5), env.replica.state.HW)
	require.Equal(t, uint64(7), env.replica.state.LEO)
}

func TestApplyFetchIgnoresEmptyRegressiveLeaderHWWithoutPromotingCommitReady(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 7

	env.replica.state.HW = 5
	env.replica.state.LEO = 7
	env.replica.state.CheckpointHW = 5
	env.replica.state.CommitReady = false
	env.replica.publishStateLocked()

	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      7,
		Leader:     1,
		LeaderHW:   4,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(5), env.replica.state.HW)
	require.Equal(t, uint64(7), env.replica.state.LEO)
	require.False(t, env.replica.state.CommitReady)
}
