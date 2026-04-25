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
