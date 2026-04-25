package replica

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestCloseStopsCollectorGoroutine(t *testing.T) {
	r := newLeaderReplica(t)
	require.NoError(t, r.Close())

	select {
	case <-r.collectorDone:
	case <-time.After(time.Second):
		t.Fatal("collector goroutine did not stop")
	}

	require.NoError(t, r.Close())
}

func TestBecomeLeaderSucceedsButAppendStaysGatedUntilCommitReady(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 3
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 1, 1)
	r.mustApplyMeta(t, meta)

	require.NoError(t, r.BecomeLeader(meta))
	st := r.Status()
	require.Equal(t, channel.ReplicaRoleLeader, st.Role)

	setReplicaStateOptionalBoolField(t, &r.state, "CommitReady", false)

	_, err := r.Append(context.Background(), []channel.Record{{Payload: []byte("gated"), SizeBytes: 1}})
	require.Error(t, err, "leader append should stay gated until commit-ready")
	require.Zero(t, env.log.appendCount, "gated leader must not append to the log")
}

func TestBecomeLeaderReconcilesLocalTailWithoutPeerProbesWhenMinISROne(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 5
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := channel.Meta{
		Key:        "group-10",
		Epoch:      8,
		Leader:     1,
		Replicas:   []channel.NodeID{1},
		ISR:        []channel.NodeID{1},
		MinISR:     1,
		LeaseUntil: time.Unix(1_700_000_300, 0).UTC(),
	}
	r.mustApplyMeta(t, meta)

	require.NoError(t, r.BecomeLeader(meta))
	require.Eventually(t, func() bool {
		st := r.Status()
		return st.CommitReady && st.HW == 5 && st.CheckpointHW == 5 && st.LEO == 5
	}, time.Second, 10*time.Millisecond, "single-node cluster should locally recover its durable tail above the checkpoint")
	require.Equal(t, uint64(5), env.checkpoints.lastStored().HW)
}

func TestBecomeLeaderReconcilesFetchedTailWhenFollowerHasRecordsAboveHW(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 3
	env.log.records = []channel.Record{{Payload: []byte("a"), SizeBytes: 1}}
	env.log.leo = 1
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.peerProofs = map[channel.NodeID]channel.ReplicaReconcileProof{
		1: {OffsetEpoch: 7, LogEndOffset: 1, CheckpointHW: 0},
		2: {OffsetEpoch: 7, LogEndOffset: 1, CheckpointHW: 0},
	}

	r := newReplicaFromEnv(t, env)
	followerMeta := activeMetaWithMinISR(7, 1, 2)
	r.mustApplyMeta(t, followerMeta)
	require.NoError(t, r.BecomeFollower(followerMeta))

	r.mu.Lock()
	r.state.LEO = 1
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.CommitReady = true
	r.publishStateLocked()
	r.mu.Unlock()

	leaderMeta := activeMetaWithMinISR(8, 3, 2)
	r.mustApplyMeta(t, leaderMeta)
	require.NoError(t, r.BecomeLeader(leaderMeta))
	require.Eventually(t, func() bool {
		st := r.Status()
		return st.CommitReady && st.HW == 1 && st.CheckpointHW == 1 && st.LEO == 1
	}, time.Second, 10*time.Millisecond, "leader reconcile should commit a quorum-visible fetched tail even if follower state previously looked commit-ready")
}

func TestBecomeLeaderCommitsFullyProvenTailWithoutWaitingForOfflinePeerProof(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 3
	env.log.records = []channel.Record{{Payload: []byte("a"), SizeBytes: 1}}
	env.log.leo = 1
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.peerProofs = map[channel.NodeID]channel.ReplicaReconcileProof{
		2: {OffsetEpoch: 7, LogEndOffset: 1, CheckpointHW: 0},
	}

	r := newReplicaFromEnv(t, env)
	followerMeta := activeMetaWithMinISR(7, 1, 2)
	r.mustApplyMeta(t, followerMeta)
	require.NoError(t, r.BecomeFollower(followerMeta))

	r.mu.Lock()
	r.state.LEO = 1
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.CommitReady = true
	r.publishStateLocked()
	r.mu.Unlock()

	leaderMeta := activeMetaWithMinISR(8, 3, 2)
	r.mustApplyMeta(t, leaderMeta)
	require.NoError(t, r.BecomeLeader(leaderMeta))
	require.Eventually(t, func() bool {
		st := r.Status()
		return st.CommitReady && st.HW == 1 && st.CheckpointHW == 1 && st.LEO == 1
	}, time.Second, 10*time.Millisecond, "leader should become ready once its local tail is fully proven by a quorum even if another ISR peer stays offline")
}
