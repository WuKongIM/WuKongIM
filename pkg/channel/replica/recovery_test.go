package replica

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestNewReplicaRetainsQuorumSafeTailUntilReconcile(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 5
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.peerProgress = map[channel.NodeID]uint64{1: 5, 2: 5, 3: 3}
	safeTail := quorumVisibleOffset(t, env.peerProgress, 2)

	r := newReplicaFromEnv(t, env)
	st := r.Status()
	require.Empty(t, env.log.truncateCalls, "startup must preserve the local tail until reconcile proves it safe")
	require.Equal(t, safeTail, st.LEO, "startup must keep the quorum-visible safe tail above CheckpointHW")
	require.Equal(t, uint64(3), st.HW)
	requireReplicaStateUint64Field(t, st, "CheckpointHW", 3)
	requireReplicaStateBoolField(t, st, "CommitReady", false)

	meta := activeMetaWithMinISR(7, 1, 2)
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))
	require.Empty(t, env.log.truncateCalls, "reconcile must retain a majority-visible tail")
	require.NoError(t, r.ApplyProgressAck(context.Background(), channel.ReplicaProgressAckRequest{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		ReplicaID:   2,
		MatchOffset: safeTail,
	}))
	require.Eventually(t, func() bool {
		st = r.Status()
		return st.LEO == safeTail && st.HW == safeTail
	}, time.Second, 10*time.Millisecond, "quorum reconcile should commit the retained safe tail")
	requireReplicaStateBoolField(t, r.Status(), "CommitReady", true)
}

func TestNewReplicaTruncatesMinorityTailDuringReconcile(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 5
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{
		{Epoch: 7, StartOffset: 0},
		{Epoch: 8, StartOffset: 4},
	}
	env.peerProgress = map[channel.NodeID]uint64{1: 5, 2: 3, 3: 3}
	safeTail := quorumVisibleOffset(t, env.peerProgress, 2)
	localTail := env.peerProgress[env.localNode]

	r := newReplicaFromEnv(t, env)
	st := r.Status()
	require.Empty(t, env.log.truncateCalls, "startup must keep the minority tail provisional until reconcile")
	require.Equal(t, localTail, st.LEO, "startup must retain the minority tail until reconcile decides it is unsafe")
	require.Equal(t, uint64(3), st.HW)
	requireReplicaStateUint64Field(t, st, "CheckpointHW", 3)
	requireReplicaStateBoolField(t, st, "CommitReady", false)

	meta := activeMetaWithMinISR(8, 1, 2)
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))
	require.Equal(t, []uint64{safeTail}, env.log.truncateCalls, "reconcile must truncate the unsafe minority tail")
	require.Eventually(t, func() bool {
		st = r.Status()
		return st.LEO == safeTail && st.HW == safeTail
	}, time.Second, 10*time.Millisecond, "minority-only tail should be cut before the leader becomes commit-ready")
	requireReplicaStateBoolField(t, r.Status(), "CommitReady", true)
}

func TestNewReplicaTruncatesSameLengthDivergentTailDuringReconcile(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 5
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{
		{Epoch: 7, StartOffset: 0},
		{Epoch: 8, StartOffset: 4},
	}
	env.peerProofs = map[channel.NodeID]channel.ReplicaReconcileProof{
		2: {OffsetEpoch: 7, LogEndOffset: 5, CheckpointHW: 3},
		3: {OffsetEpoch: 7, LogEndOffset: 3, CheckpointHW: 3},
	}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 1, 2)
	r.mustApplyMeta(t, meta)

	require.NoError(t, r.BecomeLeader(meta))
	require.Equal(t, []uint64{4}, env.log.truncateCalls, "reconcile must cut back to the highest quorum-visible prefix on the active epoch lineage")
	require.Eventually(t, func() bool {
		st := r.Status()
		return st.LEO == 4 && st.HW == 4
	}, time.Second, 10*time.Millisecond)
	requireReplicaStateBoolField(t, r.Status(), "CommitReady", true)
}

func TestRecoverFromStoresDoesNotTruncateAboveCheckpointBeforeReconcile(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 5
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	st := r.Status()

	require.Empty(t, env.log.truncateCalls, "startup must leave the tail provisional until reconcile proves it safe")
	require.Equal(t, uint64(5), st.LEO, "startup should preserve the local tail above the checkpoint")
	require.Equal(t, uint64(3), st.HW)
	requireReplicaStateBoolField(t, st, "CommitReady", false)
}

func quorumVisibleOffset(t *testing.T, peerProgress map[channel.NodeID]uint64, quorum int) uint64 {
	t.Helper()

	require.GreaterOrEqual(t, len(peerProgress), quorum, "peerProgress must include enough replicas to form a quorum")
	values := make([]uint64, 0, len(peerProgress))
	for _, offset := range peerProgress {
		values = append(values, offset)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] > values[j] })
	return values[quorum-1]
}

func TestNewReplicaRejectsCheckpointBeyondLEO(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 2
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{LogStartOffset: 0, HW: 3}

	_, err := NewReplica(env.config())
	if !errors.Is(err, channel.ErrCorruptState) {
		t.Fatalf("expected ErrCorruptState, got %v", err)
	}
}

func TestNewReplicaBootstrapsEmptyState(t *testing.T) {
	env := newTestEnv(t)
	r := newReplicaFromEnv(t, env)
	st := r.Status()
	require.Equal(t, uint64(0), st.LogStartOffset)
	require.Equal(t, uint64(0), st.HW)
	require.Equal(t, uint64(0), st.LEO)
}
