package replica

import (
	"context"
	"errors"
	"testing"

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
