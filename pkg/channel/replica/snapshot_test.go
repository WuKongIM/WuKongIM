package replica

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestInstallSnapshotPersistsCheckpointAndState(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	snap := channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("snap")}

	require.NoError(t, env.replica.InstallSnapshot(context.Background(), snap))
	state := env.replica.Status()
	require.Equal(t, uint64(8), state.HW)
	require.Equal(t, uint64(8), state.LogStartOffset)
	require.Equal(t, uint64(8), env.checkpoints.lastStored().HW)
}

func TestInstallSnapshotRejectsLogStoreBehindSnapshotEndOffset(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 5
	before := env.replica.Status()
	err := env.replica.InstallSnapshot(context.Background(), channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8})
	if !errors.Is(err, channel.ErrCorruptState) {
		t.Fatalf("expected ErrCorruptState, got %v", err)
	}
	require.Empty(t, env.snapshots.installed)
	require.Empty(t, env.checkpoints.stored)
	require.Equal(t, before, env.replica.Status())
}

func TestInstallSnapshotRejectsMismatchedChannelWithoutMutation(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	before := env.replica.Status()
	historyBefore := append([]channel.EpochPoint(nil), env.history.points...)

	err := env.replica.InstallSnapshot(context.Background(), channel.Snapshot{
		ChannelKey: "group-other",
		Epoch:      7,
		EndOffset:  8,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)
	require.Empty(t, env.snapshots.installed)
	require.Empty(t, env.checkpoints.stored)
	require.Equal(t, before, env.replica.Status())
	require.Equal(t, historyBefore, env.history.points)
}

func TestInstallSnapshotRejectsStaleEpochWithoutMutation(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	before := env.replica.Status()
	historyBefore := append([]channel.EpochPoint(nil), env.history.points...)

	err := env.replica.InstallSnapshot(context.Background(), channel.Snapshot{
		ChannelKey: "group-10",
		Epoch:      6,
		EndOffset:  8,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)
	require.Empty(t, env.snapshots.installed)
	require.Empty(t, env.checkpoints.stored)
	require.Equal(t, before, env.replica.Status())
	require.Equal(t, historyBefore, env.history.points)
}

func TestInstallSnapshotRejectsBackwardEndOffsetWithoutMutation(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	env.replica.state.HW = 6
	env.replica.state.LogStartOffset = 5
	env.replica.publishStateLocked()
	before := env.replica.Status()
	historyBefore := append([]channel.EpochPoint(nil), env.history.points...)

	err := env.replica.InstallSnapshot(context.Background(), channel.Snapshot{
		ChannelKey: "group-10",
		Epoch:      7,
		EndOffset:  4,
	})
	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Empty(t, env.snapshots.installed)
	require.Empty(t, env.checkpoints.stored)
	require.Equal(t, before, env.replica.Status())
	require.Equal(t, historyBefore, env.history.points)
}

func TestInstallSnapshotPersistsEpochHistoryForRecovery(t *testing.T) {
	env := newFollowerEnv(t)
	env.history.points = nil
	env.log.leo = 8

	snap := channel.Snapshot{
		ChannelKey: "group-10",
		Epoch:      11,
		EndOffset:  8,
		Payload:    []byte("snap"),
	}
	require.NoError(t, env.replica.InstallSnapshot(context.Background(), snap))

	reloaded := newReplicaFromEnv(t, env)
	state := reloaded.Status()
	require.Equal(t, uint64(11), state.OffsetEpoch)
	require.NotEmpty(t, env.history.points)
	last := env.history.points[len(env.history.points)-1]
	require.Equal(t, uint64(11), last.Epoch)
	require.Equal(t, uint64(8), last.StartOffset)
}

func TestInstallSnapshotStaleEffectDoesNotWriteAfterMetaChange(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica

	snap := channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("snap")}
	prepared := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{Snapshot: snap})
	require.NoError(t, prepared.Err)
	require.Len(t, prepared.Effects, 1)
	effect := prepared.Effects[0].(installSnapshotEffect)

	require.NoError(t, r.ApplyMeta(activeMetaWithMinISR(8, 1, 2)))
	err := r.executeInstallSnapshotEffect(context.Background(), effect)

	require.ErrorIs(t, err, channel.ErrStaleMeta)
	require.Empty(t, env.snapshots.installed)
	require.Empty(t, env.checkpoints.stored)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, env.history.points)
	st := r.Status()
	require.Equal(t, uint64(8), st.Epoch)
	require.Equal(t, uint64(0), st.HW)
	require.Equal(t, uint64(0), st.LogStartOffset)
	next := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{
		Snapshot: channel.Snapshot{ChannelKey: "group-10", Epoch: 8, EndOffset: 8, Payload: []byte("next")},
	})
	require.NoError(t, next.Err)
}

func TestInstallSnapshotRejectsConcurrentPendingSnapshot(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica

	first := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{
		Snapshot: channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("first")},
	})
	require.NoError(t, first.Err)
	require.Len(t, first.Effects, 1)

	second := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{
		Snapshot: channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("second")},
	})

	require.ErrorIs(t, second.Err, channel.ErrNotReady)
}

func TestInstallSnapshotEffectAfterRoleChangeIsFenced(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica

	snap := channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("snap")}
	prepared := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{Snapshot: snap})
	require.NoError(t, prepared.Err)
	require.Len(t, prepared.Effects, 1)
	effect := prepared.Effects[0].(installSnapshotEffect)

	blocking := &blockingSnapshotDurableStore{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		view: durableView{
			Checkpoint:   channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8},
			EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
			LEO:          8,
		},
	}
	r.durable = blocking

	installed := make(chan error, 1)
	go func() {
		installed <- r.executeInstallSnapshotEffect(context.Background(), effect)
	}()
	<-blocking.entered

	metaApplied := make(chan error, 1)
	go func() {
		metaApplied <- r.ApplyMeta(activeMetaWithMinISR(8, 1, 2))
	}()

	select {
	case err := <-metaApplied:
		require.NoError(t, err)
	case <-time.After(50 * time.Millisecond):
		t.Fatal("role/meta change was blocked by durable snapshot write")
	}

	close(blocking.release)
	require.NoError(t, <-installed)
	require.Equal(t, 1, blocking.writeCount())
	st := r.Status()
	require.Equal(t, uint64(8), st.Epoch)
	require.Equal(t, uint64(0), st.HW)
	require.Equal(t, uint64(0), st.LogStartOffset)
}

func TestCloseReturnsWhileSnapshotInstallInFlight(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica
	blocking := &blockingSnapshotDurableStore{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		view: durableView{
			Checkpoint:   channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8},
			EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
			LEO:          8,
		},
	}
	r.durable = blocking

	installed := make(chan error, 1)
	go func() {
		installed <- r.InstallSnapshot(context.Background(), channel.Snapshot{
			ChannelKey: "group-10",
			Epoch:      7,
			EndOffset:  8,
			Payload:    []byte("snap"),
		})
	}()
	<-blocking.entered
	r.checkpointEffects <- storeCheckpointEffect{
		EffectID:       999,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
		Checkpoint:     channel.Checkpoint{Epoch: r.state.Epoch, LogStartOffset: 0, HW: 0},
		VisibleHW:      0,
		LEO:            0,
	}
	time.Sleep(10 * time.Millisecond)

	closed := make(chan error, 1)
	go func() {
		closed <- r.Close()
	}()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		close(blocking.release)
		t.Fatal("Close blocked on in-flight snapshot install")
	}
	close(blocking.release)
	select {
	case err := <-installed:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("snapshot install did not return after release")
	}
}

func TestInstallSnapshotPublishesAfterCallerContextCanceledPostCommit(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica

	snap := channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("snap")}
	prepared := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{Snapshot: snap})
	require.NoError(t, prepared.Err)
	require.Len(t, prepared.Effects, 1)
	effect := prepared.Effects[0].(installSnapshotEffect)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.durable = &cancelAfterSnapshotWriteDurableStore{
		cancel: cancel,
		view: durableView{
			Checkpoint:   channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8},
			EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
			LEO:          8,
		},
	}

	err := r.executeInstallSnapshotEffect(ctx, effect)

	require.NoError(t, err)
	st := r.Status()
	require.Equal(t, uint64(8), st.HW)
	require.Equal(t, uint64(8), st.LogStartOffset)
	require.Equal(t, uint64(8), st.CheckpointHW)
}

func TestInstallSnapshotStaleResultIsDiscardedAfterMetaChange(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica

	snap := channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("snap")}
	prepared := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{Snapshot: snap})
	require.NoError(t, prepared.Err)
	require.Len(t, prepared.Effects, 1)
	effect := prepared.Effects[0].(installSnapshotEffect)

	require.NoError(t, r.ApplyMeta(activeMetaWithMinISR(8, 1, 2)))
	stale := r.submitLoopCommand(context.Background(), machineSnapshotInstalledEvent{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		RoleGeneration: effect.RoleGeneration,
		View: durableView{
			Checkpoint:   channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8},
			EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
			LEO:          8,
		},
	})

	require.NoError(t, stale.Err)
	st := r.Status()
	require.Equal(t, uint64(8), st.Epoch)
	require.Equal(t, uint64(0), st.HW)
	require.Equal(t, uint64(0), st.LogStartOffset)
}

func TestInstallSnapshotResultAfterTombstoneIsDiscarded(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica

	prepared := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{
		Snapshot: channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("snap")},
	})
	require.NoError(t, prepared.Err)
	effect := prepared.Effects[0].(installSnapshotEffect)

	require.NoError(t, r.Tombstone())
	stale := r.submitLoopCommand(context.Background(), machineSnapshotInstalledEvent{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		RoleGeneration: effect.RoleGeneration,
		View: durableView{
			Checkpoint:   channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8},
			EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
			LEO:          8,
		},
	})

	require.NoError(t, stale.Err)
	st := r.Status()
	require.Equal(t, channel.ReplicaRoleTombstoned, st.Role)
	require.Equal(t, uint64(0), st.HW)
	require.Equal(t, uint64(0), st.LogStartOffset)
}

func TestInstallSnapshotResultAfterCloseIsDiscarded(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica

	prepared := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{
		Snapshot: channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("snap")},
	})
	require.NoError(t, prepared.Err)
	effect := prepared.Effects[0].(installSnapshotEffect)

	require.NoError(t, r.applyCloseCommand().Err)
	stale := r.applyLoopEvent(machineSnapshotInstalledEvent{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		RoleGeneration: effect.RoleGeneration,
		View: durableView{
			Checkpoint:   channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8},
			EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
			LEO:          8,
		},
	})

	require.NoError(t, stale.Err)
	st := r.Status()
	require.Equal(t, uint64(0), st.HW)
	require.Equal(t, uint64(0), st.LogStartOffset)
	require.True(t, r.closed)
}

func TestInstallSnapshotResultInvariantFailureRestoresEpochHistory(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	r := env.replica

	prepared := r.submitLoopCommand(context.Background(), machineInstallSnapshotCommand{
		Snapshot: channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 8, Payload: []byte("snap")},
	})
	require.NoError(t, prepared.Err)
	effect := prepared.Effects[0].(installSnapshotEffect)
	beforeHistory := append([]channel.EpochPoint(nil), r.epochHistory...)

	result := r.submitLoopCommand(context.Background(), machineSnapshotInstalledEvent{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		RoleGeneration: effect.RoleGeneration,
		View: durableView{
			Checkpoint: channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8},
			EpochHistory: []channel.EpochPoint{
				{Epoch: 7, StartOffset: 0},
				{Epoch: 7, StartOffset: 8},
			},
			LEO: 8,
		},
	})

	require.ErrorIs(t, result.Err, channel.ErrCorruptState)
	require.Equal(t, beforeHistory, r.epochHistory)
	require.Equal(t, uint64(0), r.Status().HW)
}

type blockingSnapshotDurableStore struct {
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	view    durableView
	writes  int
	once    sync.Once
}

func (s *blockingSnapshotDurableStore) Recover(context.Context) (durableView, error) {
	return s.view, nil
}

func (s *blockingSnapshotDurableStore) BeginEpoch(context.Context, channel.EpochPoint, uint64) error {
	return channel.ErrInvalidArgument
}

func (s *blockingSnapshotDurableStore) AppendLeaderBatch(context.Context, []channel.Record) (uint64, uint64, error) {
	return 0, 0, channel.ErrInvalidArgument
}

func (s *blockingSnapshotDurableStore) ApplyFollowerBatch(context.Context, channel.ApplyFetchStoreRequest, *channel.EpochPoint) (uint64, error) {
	return 0, channel.ErrInvalidArgument
}

func (s *blockingSnapshotDurableStore) TruncateLogAndHistory(context.Context, uint64) error {
	return channel.ErrInvalidArgument
}

func (s *blockingSnapshotDurableStore) StoreCheckpointMonotonic(context.Context, channel.Checkpoint, uint64, uint64) error {
	return channel.ErrInvalidArgument
}

func (s *blockingSnapshotDurableStore) InstallSnapshotAtomically(ctx context.Context, _ channel.Snapshot, _ channel.Checkpoint, _ channel.EpochPoint) (uint64, error) {
	s.once.Do(func() {
		close(s.entered)
	})
	select {
	case <-s.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return s.view.LEO, nil
}

func (s *blockingSnapshotDurableStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

type cancelAfterSnapshotWriteDurableStore struct {
	view   durableView
	cancel context.CancelFunc
}

func (s *cancelAfterSnapshotWriteDurableStore) Recover(ctx context.Context) (durableView, error) {
	if err := ctx.Err(); err != nil {
		return durableView{}, err
	}
	return s.view, nil
}

func (s *cancelAfterSnapshotWriteDurableStore) BeginEpoch(context.Context, channel.EpochPoint, uint64) error {
	return channel.ErrInvalidArgument
}

func (s *cancelAfterSnapshotWriteDurableStore) AppendLeaderBatch(context.Context, []channel.Record) (uint64, uint64, error) {
	return 0, 0, channel.ErrInvalidArgument
}

func (s *cancelAfterSnapshotWriteDurableStore) ApplyFollowerBatch(context.Context, channel.ApplyFetchStoreRequest, *channel.EpochPoint) (uint64, error) {
	return 0, channel.ErrInvalidArgument
}

func (s *cancelAfterSnapshotWriteDurableStore) TruncateLogAndHistory(context.Context, uint64) error {
	return channel.ErrInvalidArgument
}

func (s *cancelAfterSnapshotWriteDurableStore) StoreCheckpointMonotonic(context.Context, channel.Checkpoint, uint64, uint64) error {
	return channel.ErrInvalidArgument
}

func (s *cancelAfterSnapshotWriteDurableStore) InstallSnapshotAtomically(context.Context, channel.Snapshot, channel.Checkpoint, channel.EpochPoint) (uint64, error) {
	s.cancel()
	return s.view.LEO, nil
}
