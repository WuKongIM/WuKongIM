package replica

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestStatusReturnsLatestReplicaSnapshot(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	done := make(chan channel.CommitResult, 1)
	go func() {
		res, err := cluster.leader.Append(context.Background(), []channel.Record{{Payload: []byte("a"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 1)

	deadline := time.After(time.Second)
	for {
		state := cluster.leader.Status()
		if state.LEO == 1 {
			require.Equal(t, uint64(0), state.HW)
			break
		}
		select {
		case <-deadline:
			t.Fatal("Status() did not expose latest LEO snapshot while append was waiting for quorum")
		default:
		}
	}
	select {
	case <-done:
		t.Fatal("append returned before MinISR quorum")
	default:
	}

	cluster.replicateOnce(t, cluster.follower2)
	cluster.replicateOnce(t, cluster.follower3)
	<-done
}

func TestStatusIsUpdatedAfterFollowerAckAdvancesHW(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	done := make(chan channel.CommitResult, 1)
	go func() {
		res, err := cluster.leader.Append(context.Background(), []channel.Record{{Payload: []byte("m"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 1)

	cluster.replicateOnce(t, cluster.follower2)
	cluster.replicateOnce(t, cluster.follower3)
	<-done

	state := cluster.leader.Status()
	require.Equal(t, uint64(1), state.HW)
	require.Equal(t, uint64(1), state.LEO)
}

func TestProgressCursorUpdateIgnoresRegressingMatchOffset(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	cluster.leader.mu.Lock()
	cluster.leader.state.LEO = 3
	cluster.leader.state.OffsetEpoch = 7
	cluster.leader.progress[cluster.leader.localNode] = 3
	cluster.leader.mu.Unlock()

	require.NoError(t, cluster.leader.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  cluster.leader.state.ChannelKey,
		Epoch:       cluster.leader.state.Epoch,
		ReplicaID:   cluster.follower2.localNode,
		MatchOffset: 3,
		OffsetEpoch: cluster.leader.state.OffsetEpoch,
	}))
	require.NoError(t, cluster.leader.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  cluster.leader.state.ChannelKey,
		Epoch:       cluster.leader.state.Epoch,
		ReplicaID:   cluster.follower2.localNode,
		MatchOffset: 2,
		OffsetEpoch: cluster.leader.state.OffsetEpoch,
	}))

	cluster.leader.mu.RLock()
	defer cluster.leader.mu.RUnlock()
	require.Equal(t, uint64(3), cluster.leader.progress[cluster.follower2.localNode])
}

func TestStatusReportsCommitAndCheckpointWatermarksSeparately(t *testing.T) {
	t.Run("not ready", func(t *testing.T) {
		env := newTestEnv(t)
		r := newReplicaFromEnv(t, env)

		r.mu.Lock()
		r.state.HW = 5
		r.state.LEO = 5
		setReplicaStateOptionalUint64Field(t, &r.state, "CheckpointHW", 3)
		setReplicaStateOptionalBoolField(t, &r.state, "CommitReady", false)
		r.publishStateLocked()
		r.mu.Unlock()

		st := r.Status()
		require.Equal(t, uint64(5), st.HW)
		require.Equal(t, uint64(5), st.LEO)
		requireReplicaStateUint64Field(t, st, "CheckpointHW", 3)
		requireReplicaStateBoolField(t, st, "CommitReady", false)
	})

	t.Run("ready", func(t *testing.T) {
		env := newTestEnv(t)
		r := newReplicaFromEnv(t, env)

		r.mu.Lock()
		r.state.HW = 5
		r.state.LEO = 5
		setReplicaStateOptionalUint64Field(t, &r.state, "CheckpointHW", 5)
		setReplicaStateOptionalBoolField(t, &r.state, "CommitReady", true)
		r.publishStateLocked()
		r.mu.Unlock()

		st := r.Status()
		require.Equal(t, uint64(5), st.HW)
		require.Equal(t, uint64(5), st.LEO)
		requireReplicaStateUint64Field(t, st, "CheckpointHW", 5)
		requireReplicaStateBoolField(t, st, "CommitReady", true)
	})
}
