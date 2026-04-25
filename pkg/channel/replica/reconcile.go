package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) beginLeaderReconcileLocked() {
	if !r.needsLeaderReconcileLocked() {
		r.reconcilePending = nil
		return
	}
	r.state.CommitReady = false
	if !r.needsLeaderProofsLocked() {
		r.reconcilePending = nil
		return
	}

	pending := make(map[channel.NodeID]struct{}, len(r.meta.ISR))
	for _, id := range r.meta.ISR {
		if id == 0 || id == r.localNode {
			continue
		}
		pending[id] = struct{}{}
	}
	r.reconcilePending = pending
}

func (r *replica) needsLeaderReconcileLocked() bool {
	if !r.state.CommitReady {
		return true
	}
	if r.state.LEO > r.state.HW {
		return true
	}
	return r.state.CheckpointHW < r.state.HW
}

func (r *replica) needsLeaderProofsLocked() bool {
	return r.state.LEO > r.state.HW
}

func (r *replica) runConfiguredLeaderReconcile(ctx context.Context, meta channel.Meta, source ReconcileProbeSource) error {
	if source == nil {
		return nil
	}
	local := r.Status()
	proofs, err := source.ProbeQuorum(ctx, meta, local)
	if err != nil {
		return err
	}
	for _, proof := range proofs {
		if err := r.ApplyReconcileProof(ctx, proof); err != nil {
			return err
		}
	}
	return nil
}

func (r *replica) ApplyReconcileProof(_ context.Context, proof channel.ReplicaReconcileProof) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		return channel.ErrNotLeader
	}
	if proof.ChannelKey != "" && r.state.ChannelKey != "" && proof.ChannelKey != r.state.ChannelKey {
		return channel.ErrStaleMeta
	}
	if proof.Epoch != 0 && proof.Epoch != r.state.Epoch {
		return channel.ErrStaleMeta
	}
	if proof.ReplicaID == 0 || proof.ReplicaID == r.localNode {
		return channel.ErrInvalidMeta
	}
	if proof.LogEndOffset > r.state.LEO {
		return channel.ErrCorruptState
	}
	matchOffset, err := r.reconcileMatchOffsetLocked(proof)
	if err != nil {
		return err
	}

	r.setReplicaProgressLocked(proof.ReplicaID, matchOffset)
	if len(r.reconcilePending) == 0 {
		r.publishStateLocked()
		return nil
	}
	delete(r.reconcilePending, proof.ReplicaID)
	if len(r.reconcilePending) != 0 && !r.localTailFullyProvenLocked() {
		r.publishStateLocked()
		return nil
	}

	return r.completeLeaderReconcileLocked()
}

func (r *replica) runLocalLeaderReconcile() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.completeLeaderReconcileLocked()
}

func (r *replica) completeLeaderReconcileLocked() error {
	candidate, err := r.reconcileCandidateLocked()
	if err != nil {
		return err
	}
	if candidate < r.state.HW {
		return channel.ErrCorruptState
	}
	if candidate > r.state.LEO {
		return channel.ErrCorruptState
	}
	if candidate < r.state.LEO {
		if err := r.truncateLogToLocked(candidate); err != nil {
			return err
		}
		r.state.LEO = candidate
		r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, candidate)
		r.setReplicaProgressLocked(r.localNode, candidate)
	}

	checkpoint := channel.Checkpoint{
		Epoch:          r.state.Epoch,
		LogStartOffset: r.state.LogStartOffset,
		HW:             candidate,
	}
	if err := r.checkpoints.Store(checkpoint); err != nil {
		r.publishStateLocked()
		return err
	}

	r.state.HW = candidate
	r.state.CheckpointHW = candidate
	r.state.CommitReady = true
	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, r.state.LEO)
	r.reconcilePending = nil
	r.publishStateLocked()
	return nil
}

func (r *replica) localTailFullyProvenLocked() bool {
	if r.state.LEO <= r.state.HW {
		return true
	}
	checkpoint, candidate, err := r.nextHWCheckpointLocked()
	if err != nil || checkpoint == nil {
		return false
	}
	return candidate == r.state.LEO
}

func (r *replica) reconcileMatchOffsetLocked(proof channel.ReplicaReconcileProof) (uint64, error) {
	if proof.CheckpointHW > proof.LogEndOffset {
		return 0, channel.ErrCorruptState
	}
	matchOffset, _ := r.divergenceStateLocked(proof.LogEndOffset, proof.OffsetEpoch, r.state.LEO)
	if matchOffset > proof.LogEndOffset || matchOffset > r.state.LEO {
		return 0, channel.ErrCorruptState
	}
	return matchOffset, nil
}

func (r *replica) reconcileCandidateLocked() (uint64, error) {
	checkpoint, candidate, err := r.nextHWCheckpointLocked()
	if err != nil {
		return 0, err
	}
	if checkpoint == nil {
		return r.state.HW, nil
	}
	return candidate, nil
}
