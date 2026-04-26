package replica

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestApplyReconcileProofRejectsFutureOffsetEpoch(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica

	r.mu.Lock()
	r.progress[2] = 4
	r.mu.Unlock()

	err := r.ApplyReconcileProof(context.Background(), channel.ReplicaReconcileProof{
		ChannelKey:   "group-10",
		Epoch:        7,
		LeaderEpoch:  r.meta.LeaderEpoch,
		ReplicaID:    2,
		OffsetEpoch:  99,
		LogEndOffset: 5,
		CheckpointHW: 4,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)

	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}

func TestApplyReconcileProofRejectsFutureOffsetEpochBeforeLeaderLEOGuard(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica
	var overLeaderLEO uint64

	r.mu.Lock()
	r.progress[2] = 4
	overLeaderLEO = r.state.LEO + 1
	r.mu.Unlock()

	err := r.ApplyReconcileProof(context.Background(), channel.ReplicaReconcileProof{
		ChannelKey:   "group-10",
		Epoch:        7,
		LeaderEpoch:  r.meta.LeaderEpoch,
		ReplicaID:    2,
		OffsetEpoch:  99,
		LogEndOffset: overLeaderLEO,
		CheckpointHW: 4,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)

	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}

func TestApplyReconcileProofCapsKnownEpochOverLeaderLEO(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica
	var leaderLEO uint64

	r.mu.Lock()
	r.progress[2] = 4
	leaderLEO = r.state.LEO
	r.mu.Unlock()

	err := r.ApplyReconcileProof(context.Background(), channel.ReplicaReconcileProof{
		ChannelKey:   "group-10",
		Epoch:        7,
		LeaderEpoch:  r.meta.LeaderEpoch,
		ReplicaID:    2,
		OffsetEpoch:  7,
		LogEndOffset: leaderLEO + 1,
		CheckpointHW: 4,
	})
	require.NoError(t, err)

	r.mu.RLock()
	require.Equal(t, leaderLEO, r.progress[2])
	r.mu.RUnlock()
}

func TestBecomeLeaderReconcileRejectsProofWithoutLeaderEpoch(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 3
	env.log.records = []channel.Record{{Payload: []byte("a"), SizeBytes: 1}}
	env.log.leo = 1
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	r.probeSource = staticReconcileProofSource{proofs: []channel.ReplicaReconcileProof{{
		ChannelKey:   "group-10",
		Epoch:        8,
		ReplicaID:    2,
		OffsetEpoch:  7,
		LogEndOffset: 1,
		CheckpointHW: 0,
	}}}
	meta := activeMetaWithMinISR(8, 3, 2)
	meta.LeaderEpoch = 2
	r.mustApplyMeta(t, meta)

	err := r.BecomeLeader(meta)

	require.ErrorIs(t, err, channel.ErrStaleMeta)
	r.mu.RLock()
	require.Zero(t, r.progress[2])
	r.mu.RUnlock()
}

type staticReconcileProofSource struct {
	proofs []channel.ReplicaReconcileProof
	err    error
}

func (s staticReconcileProofSource) ProbeQuorum(context.Context, channel.Meta, channel.ReplicaState) ([]channel.ReplicaReconcileProof, error) {
	return append([]channel.ReplicaReconcileProof(nil), s.proofs...), s.err
}
