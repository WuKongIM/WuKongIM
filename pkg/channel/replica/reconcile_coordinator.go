package replica

import (
	"context"
	"slices"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const smallISRProgressBufferSize = 8

// reconcileQuorumCandidate returns the prefix that is safe for a leader reconcile round.
// Missing ISR members are counted at the already committed HW and never above it.
func reconcileQuorumCandidate(isr []channel.NodeID, matchOffsets map[channel.NodeID]uint64, minISR int, hw, leo uint64) (uint64, bool, error) {
	if len(isr) == 0 || minISR <= 0 || minISR > len(isr) {
		return 0, false, nil
	}

	var smallMatches [smallISRProgressBufferSize]uint64
	matches := smallMatches[:0]
	if len(isr) > smallISRProgressBufferSize {
		matches = make([]uint64, 0, len(isr))
	}
	for _, id := range isr {
		matchOffset, ok := matchOffsets[id]
		if !ok {
			matchOffset = hw
		}
		matches = append(matches, matchOffset)
	}

	slices.Sort(matches)
	candidate := matches[len(matches)-minISR]
	if candidate > leo {
		return 0, false, channel.ErrCorruptState
	}
	return candidate, true, nil
}

// quorumProgressCandidate returns the quorum-visible HW advancement candidate.
func quorumProgressCandidate(isr []channel.NodeID, progress map[channel.NodeID]uint64, minISR int, hw, leo uint64) (uint64, bool, error) {
	candidate, ok, err := reconcileQuorumCandidate(isr, progress, minISR, hw, leo)
	if err != nil || !ok {
		return 0, false, err
	}
	if candidate < hw {
		return 0, false, channel.ErrCorruptState
	}
	if candidate == hw {
		return 0, false, nil
	}
	return candidate, true, nil
}

func reconcileProofMatchOffset(history []channel.EpochPoint, logStartOffset, currentHW, leaderLEO uint64, proof channel.ReplicaReconcileProof) (uint64, error) {
	if proof.CheckpointHW > proof.LogEndOffset {
		return 0, channel.ErrCorruptState
	}
	decision := decideLineage(history, logStartOffset, currentHW, leaderLEO, proof.LogEndOffset, proof.OffsetEpoch)
	if decision.err != nil {
		return 0, decision.err
	}
	if decision.matchOffset > proof.LogEndOffset || decision.matchOffset > leaderLEO {
		return 0, channel.ErrCorruptState
	}
	return decision.matchOffset, nil
}

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

func (r *replica) completeLeaderReconcileLocked() machineResult {
	if !r.needsLeaderReconcileLocked() {
		r.reconcilePending = nil
		return machineResult{}
	}
	candidate, err := r.reconcileCandidateLocked()
	if err != nil {
		return machineResult{Err: err}
	}
	if candidate < r.state.HW {
		return machineResult{Err: channel.ErrCorruptState}
	}
	if candidate > r.state.LEO {
		return machineResult{Err: channel.ErrCorruptState}
	}

	checkpoint := channel.Checkpoint{
		Epoch:          r.state.Epoch,
		LogStartOffset: r.state.LogStartOffset,
		HW:             candidate,
	}
	newLEO := r.state.LEO
	var truncateTo *uint64
	if candidate < r.state.LEO {
		value := candidate
		truncateTo = &value
		newLEO = candidate
	}
	if truncateTo == nil && r.state.CheckpointHW >= candidate {
		r.state.HW = candidate
		r.state.CheckpointHW = candidate
		r.state.CommitReady = true
		r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, r.state.LEO)
		r.reconcilePending = nil
		r.publishStateLocked()
		return machineResult{}
	}
	if r.pendingReconcileEffectID != 0 {
		return machineResult{Err: channel.ErrNotReady}
	}
	effectID := r.nextLoopEffectID()
	r.pendingReconcileEffectID = effectID
	return machineResult{Effects: []machineEffect{leaderReconcileDurableEffect{
		EffectID:       effectID,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
		TruncateTo:     cloneUint64Pointer(truncateTo),
		NewLEO:         newLEO,
		HW:             candidate,
		Checkpoint:     checkpoint,
	}}}
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
	return reconcileProofMatchOffset(r.epochHistory, r.state.LogStartOffset, r.state.HW, r.state.LEO, proof)
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

func (r *replica) ensureReconcileLeaseLocked() error {
	if r.state.Role == channel.ReplicaRoleFencedLeader {
		return channel.ErrLeaseExpired
	}
	if r.now().Before(r.meta.LeaseUntil) {
		return nil
	}
	r.state.Role = channel.ReplicaRoleFencedLeader
	r.reconcilePending = nil
	r.pendingReconcileEffectID = 0
	r.roleGeneration++
	r.publishStateLocked()
	return channel.ErrLeaseExpired
}

func (r *replica) executeLeaderReconcileDurableEffect(ctx context.Context, effect leaderReconcileDurableEffect) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	fenceErr := r.validateLeaderReconcileEffectFenceLocked(effect)
	r.mu.Unlock()
	if fenceErr != nil {
		result := r.submitLoopCommand(context.Background(), machineLeaderReconcileResultCommand{
			EffectID:       effect.EffectID,
			ChannelKey:     effect.ChannelKey,
			Epoch:          effect.Epoch,
			LeaderEpoch:    effect.LeaderEpoch,
			RoleGeneration: effect.RoleGeneration,
			StoredLEO:      effect.NewLEO,
			HW:             effect.HW,
			Checkpoint:     effect.Checkpoint,
			TruncateTo:     cloneUint64Pointer(effect.TruncateTo),
			Err:            fenceErr,
		})
		if result.Err != nil {
			return result.Err
		}
		return fenceErr
	}

	var (
		truncated bool
		err       error
	)
	if err = r.lockDurableMu(ctx); err == nil {
		r.mu.Lock()
		err = r.validateLeaderReconcileEffectFenceLocked(effect)
		r.mu.Unlock()
		if err == nil && effect.TruncateTo != nil {
			err = r.durable.TruncateLogAndHistory(ctx, *effect.TruncateTo)
			truncated = err == nil
		}
		if err == nil {
			err = r.durable.StoreCheckpointMonotonic(ctx, effect.Checkpoint, effect.HW, effect.NewLEO)
		}
		r.durableMu.Unlock()
	}

	result := r.submitLoopCommand(context.Background(), machineLeaderReconcileResultCommand{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		LeaderEpoch:    effect.LeaderEpoch,
		RoleGeneration: effect.RoleGeneration,
		StoredLEO:      effect.NewLEO,
		HW:             effect.HW,
		Checkpoint:     effect.Checkpoint,
		Truncated:      truncated,
		TruncateTo:     cloneUint64Pointer(effect.TruncateTo),
		Err:            err,
	})
	return result.Err
}

func (r *replica) validateLeaderReconcileEffectFenceLocked(effect leaderReconcileDurableEffect) error {
	if r.pendingReconcileEffectID == 0 || effect.EffectID != r.pendingReconcileEffectID {
		return channel.ErrStaleMeta
	}
	if r.closed {
		return channel.ErrNotLeader
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if effect.ChannelKey != r.state.ChannelKey || effect.Epoch != r.state.Epoch ||
		effect.LeaderEpoch != r.meta.LeaderEpoch || effect.RoleGeneration != r.roleGeneration {
		return channel.ErrStaleMeta
	}
	if r.state.Role == channel.ReplicaRoleFencedLeader {
		return channel.ErrLeaseExpired
	}
	if !r.now().Before(r.meta.LeaseUntil) {
		return channel.ErrLeaseExpired
	}
	return nil
}

func (r *replica) applyLeaderReconcileResultCommand(cmd machineLeaderReconcileResultCommand) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pendingReconcileEffectID == 0 || cmd.EffectID != r.pendingReconcileEffectID {
		return machineResult{}
	}
	r.pendingReconcileEffectID = 0
	if r.closed || r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{}
	}
	if cmd.ChannelKey != r.state.ChannelKey || cmd.Epoch != r.state.Epoch ||
		cmd.LeaderEpoch != r.meta.LeaderEpoch || cmd.RoleGeneration != r.roleGeneration {
		return machineResult{}
	}
	if err := r.applyReconcileDurableTruncateResultLocked(cmd); err != nil {
		return machineResult{Err: err}
	}
	if cmd.Err != nil {
		if cmd.Err == channel.ErrLeaseExpired {
			r.state.Role = channel.ReplicaRoleFencedLeader
			r.reconcilePending = nil
			r.roleGeneration++
		}
		r.state.CommitReady = false
		r.publishStateLocked()
		return machineResult{Err: cmd.Err}
	}
	if cmd.HW < r.state.HW || cmd.HW > cmd.StoredLEO || cmd.Checkpoint.HW != cmd.HW {
		return machineResult{Err: channel.ErrCorruptState}
	}
	if cmd.StoredLEO > r.state.LEO {
		return machineResult{Err: channel.ErrCorruptState}
	}

	oldHW := r.state.HW
	r.state.LEO = cmd.StoredLEO
	r.state.HW = cmd.HW
	r.state.CheckpointHW = cmd.Checkpoint.HW
	r.state.CommitReady = true
	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, r.state.LEO)
	r.setReplicaProgressLocked(r.localNode, r.state.LEO)
	r.reconcilePending = nil
	r.publishStateLocked()
	if cmd.HW > oldHW && r.onLeaderHWAdvance != nil {
		go r.onLeaderHWAdvance()
	}
	return machineResult{}
}

func (r *replica) applyReconcileDurableTruncateResultLocked(cmd machineLeaderReconcileResultCommand) error {
	if !cmd.Truncated || cmd.TruncateTo == nil {
		return nil
	}
	truncateTo := *cmd.TruncateTo
	if truncateTo < r.state.HW {
		return channel.ErrCorruptState
	}
	if r.state.LEO > truncateTo {
		r.state.LEO = truncateTo
	}
	r.epochHistory = trimEpochHistoryToLEO(r.epochHistory, truncateTo)
	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, r.state.LEO)
	r.setReplicaProgressLocked(r.localNode, r.state.LEO)
	r.publishStateLocked()
	return nil
}
