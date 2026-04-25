package replica

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestApplyFetchAdvancesCheckpointToMinLeaderHWAndLEO(t *testing.T) {
	env := newFollowerEnv(t)
	err := env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: "group-10",
		Epoch:      7,
		Leader:     1,
		Records:    []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		LeaderHW:   10,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), env.checkpoints.lastStored().HW)
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
