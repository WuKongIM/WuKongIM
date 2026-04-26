package replica

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestCursorDeltaAdvancesHWWithoutFollowUpFetch(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	done := make(chan channel.CommitResult, 1)
	go func() {
		res, err := cluster.leader.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 1)

	replicateFollowerAndAck := func(follower *replica) {
		fetch, err := cluster.leader.Fetch(context.Background(), channel.ReplicaFetchRequest{
			ChannelKey:  cluster.leader.state.ChannelKey,
			Epoch:       cluster.leader.state.Epoch,
			ReplicaID:   follower.localNode,
			FetchOffset: follower.state.LEO,
			OffsetEpoch: follower.state.OffsetEpoch,
			MaxBytes:    1024,
		})
		require.NoError(t, err)
		require.NoError(t, follower.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
			ChannelKey: cluster.leader.state.ChannelKey,
			Epoch:      fetch.Epoch,
			Leader:     cluster.leader.localNode,
			TruncateTo: fetch.TruncateTo,
			Records:    fetch.Records,
			LeaderHW:   fetch.HW,
		}))
		require.NoError(t, cluster.leader.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
			ChannelKey:  cluster.leader.state.ChannelKey,
			Epoch:       cluster.leader.state.Epoch,
			ReplicaID:   follower.localNode,
			MatchOffset: follower.state.LEO,
			OffsetEpoch: follower.state.OffsetEpoch,
		}))
	}

	replicateFollowerAndAck(cluster.follower2)
	select {
	case <-done:
		t.Fatal("append returned before MinISR was satisfied")
	default:
	}

	replicateFollowerAndAck(cluster.follower3)
	res := <-done
	require.Equal(t, uint64(1), res.NextCommitHW)
}

func TestCursorDeltaIgnoresStaleEpoch(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	err := env.replica.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  "group-10",
		Epoch:       6,
		ReplicaID:   2,
		MatchOffset: 5,
		OffsetEpoch: 7,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)
}

func TestCursorDeltaWithDivergentOffsetEpochDoesNotAdvanceHW(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica

	r.mu.Lock()
	r.meta.MinISR = 2
	r.state.HW = 4
	r.state.CheckpointHW = 4
	r.state.LEO = 6
	r.progress[r.localNode] = 6
	r.progress[2] = 4
	r.progress[3] = 4
	r.publishStateLocked()
	r.mu.Unlock()

	err := r.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		MatchOffset: 6,
		OffsetEpoch: 4,
	})
	require.NoError(t, err)

	st := r.Status()
	require.Equal(t, uint64(4), st.HW)
	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}

func TestCursorDeltaRejectsFutureOffsetEpoch(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica
	var futureMatch uint64

	r.mu.Lock()
	r.meta.MinISR = 2
	r.state.HW = 4
	r.state.CheckpointHW = 4
	r.state.LEO = 6
	r.progress[r.localNode] = 6
	r.progress[2] = 4
	r.progress[3] = 4
	futureMatch = r.state.LEO + 1
	r.publishStateLocked()
	r.mu.Unlock()

	err := r.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		MatchOffset: futureMatch,
		OffsetEpoch: 99,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)

	st := r.Status()
	require.Equal(t, uint64(4), st.HW)
	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}

func TestLegacyProgressAckWithHistoryCannotAdvanceHW(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica
	var overLeaderLEO uint64

	r.mu.Lock()
	r.meta.MinISR = 2
	r.state.HW = 4
	r.state.CheckpointHW = 4
	r.state.LEO = 6
	r.progress[r.localNode] = 6
	r.progress[2] = 4
	r.progress[3] = 4
	overLeaderLEO = r.state.LEO + 1
	r.publishStateLocked()
	r.mu.Unlock()

	err := r.ApplyProgressAck(context.Background(), channel.ReplicaProgressAckRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		MatchOffset: overLeaderLEO,
	})
	require.NoError(t, err)

	st := r.Status()
	require.Equal(t, uint64(4), st.HW)
	r.mu.RLock()
	require.LessOrEqual(t, r.progress[2], uint64(4))
	r.mu.RUnlock()
}

func TestCursorDeltaWithUnknownOffsetEpochCapsOverLeaderLEO(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica
	var overLeaderLEO uint64

	r.mu.Lock()
	r.meta.MinISR = 2
	r.state.HW = 4
	r.state.CheckpointHW = 4
	r.state.LEO = 6
	r.progress[r.localNode] = 6
	r.progress[2] = 4
	r.progress[3] = 4
	overLeaderLEO = r.state.LEO + 1
	r.publishStateLocked()
	r.mu.Unlock()

	err := r.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		MatchOffset: overLeaderLEO,
		OffsetEpoch: 4,
	})
	require.NoError(t, err)

	st := r.Status()
	require.Equal(t, uint64(4), st.HW)
	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}
