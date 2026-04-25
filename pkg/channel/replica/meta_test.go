package replica

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestApplyMetaRejectsInvalidISRSubset(t *testing.T) {
	r := newTestReplica(t)

	err := r.ApplyMeta(channel.Meta{
		Key:      "group-10",
		Epoch:    1,
		Leader:   1,
		Replicas: []channel.NodeID{1, 2},
		ISR:      []channel.NodeID{1, 3},
		MinISR:   2,
	})
	require.ErrorIs(t, err, channel.ErrInvalidMeta)
}

func TestApplyMetaNormalizesReplicaAndISRLists(t *testing.T) {
	r := newTestReplica(t)

	err := r.ApplyMeta(channel.Meta{
		Key:      "group-10",
		Epoch:    3,
		Leader:   2,
		Replicas: []channel.NodeID{3, 2, 3, 1, 2},
		ISR:      []channel.NodeID{2, 1, 2},
		MinISR:   2,
	})
	require.NoError(t, err)
	require.Equal(t, []channel.NodeID{3, 2, 1}, r.meta.Replicas)
	require.Equal(t, []channel.NodeID{2, 1}, r.meta.ISR)
}

func TestApplyMetaAcceptsSameEpochLeaderTransferWithHigherLeaderEpoch(t *testing.T) {
	r := newTestReplica(t)
	initial := channel.Meta{
		Key:         "group-10",
		Epoch:       4,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channel.NodeID{1, 2},
		ISR:         []channel.NodeID{1, 2},
		MinISR:      2,
		LeaseUntil:  time.Now().Add(time.Minute),
	}
	require.NoError(t, r.ApplyMeta(initial))

	next := initial
	next.Leader = 2
	next.LeaderEpoch = 2
	require.NoError(t, r.ApplyMeta(next))

	st := r.Status()
	require.Equal(t, channel.NodeID(2), st.Leader)
	require.Equal(t, uint64(4), st.Epoch)
}

func TestBecomeFollowerAppliesMetaAndRole(t *testing.T) {
	r := newTestReplica(t)
	err := r.BecomeFollower(channel.Meta{
		Key:      "group-10",
		Epoch:    4,
		Leader:   2,
		Replicas: []channel.NodeID{1, 2},
		ISR:      []channel.NodeID{1, 2},
		MinISR:   2,
	})
	require.NoError(t, err)

	st := r.Status()
	require.Equal(t, channel.ReplicaRoleFollower, st.Role)
	require.Equal(t, channel.NodeID(2), st.Leader)
	require.Equal(t, uint64(4), st.Epoch)
	require.Equal(t, channel.ChannelKey("group-10"), st.ChannelKey)
}

func TestBecomeLeaderAcceptsSameEpochLeaderTransferWithHigherLeaderEpoch(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 2
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	r := newReplicaFromEnv(t, env)

	initial := channel.Meta{
		Key:         "group-10",
		Epoch:       7,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channel.NodeID{1, 2, 3},
		ISR:         []channel.NodeID{1, 2, 3},
		MinISR:      2,
		LeaseUntil:  time.Now().Add(time.Minute),
	}
	require.NoError(t, r.ApplyMeta(initial))
	require.NoError(t, r.BecomeFollower(initial))

	next := initial
	next.Leader = 2
	next.LeaderEpoch = 2
	require.NoError(t, r.BecomeLeader(next))

	st := r.Status()
	require.Equal(t, channel.ReplicaRoleLeader, st.Role)
	require.Equal(t, channel.NodeID(2), st.Leader)
}

func TestBecomeFollowerAcceptsSameEpochLeaderTransferWithHigherLeaderEpoch(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 1
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	r := newReplicaFromEnv(t, env)

	initial := channel.Meta{
		Key:         "group-10",
		Epoch:       7,
		LeaderEpoch: 1,
		Leader:      1,
		Replicas:    []channel.NodeID{1, 2, 3},
		ISR:         []channel.NodeID{1, 2, 3},
		MinISR:      2,
		LeaseUntil:  time.Now().Add(time.Minute),
	}
	require.NoError(t, r.ApplyMeta(initial))
	require.NoError(t, r.BecomeLeader(initial))

	next := initial
	next.Leader = 2
	next.LeaderEpoch = 2
	require.NoError(t, r.BecomeFollower(next))

	st := r.Status()
	require.Equal(t, channel.ReplicaRoleFollower, st.Role)
	require.Equal(t, channel.NodeID(2), st.Leader)
}

func TestTombstoneFencesFutureOperations(t *testing.T) {
	r := newTestReplica(t)
	require.NoError(t, r.Tombstone())

	_, err := r.Append(context.Background(), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
	if !errors.Is(err, channel.ErrTombstoned) {
		t.Fatalf("expected ErrTombstoned, got %v", err)
	}
}
