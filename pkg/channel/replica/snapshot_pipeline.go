package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) applyInstallSnapshotCommand(cmd machineInstallSnapshotCommand) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := cmd.Snapshot
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	if r.state.ChannelKey != "" && snap.ChannelKey != "" && snap.ChannelKey != r.state.ChannelKey {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if snap.Epoch < r.state.Epoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if snap.EndOffset < r.state.HW || snap.EndOffset < r.state.LogStartOffset {
		return machineResult{Err: channel.ErrCorruptState}
	}
	if r.pendingSnapshotEffectID != 0 {
		return machineResult{Err: channel.ErrNotReady}
	}
	leo := r.log.LEO()
	if leo < snap.EndOffset {
		return machineResult{Err: channel.ErrCorruptState}
	}

	trimmedHistory := trimEpochHistoryToLEO(r.epochHistory, snap.EndOffset)
	epochPoint := channel.EpochPoint{
		Epoch:       snap.Epoch,
		StartOffset: snap.EndOffset,
	}
	if len(trimmedHistory) > 0 {
		last := trimmedHistory[len(trimmedHistory)-1]
		switch {
		case last.Epoch == snap.Epoch:
			epochPoint = last
		case last.Epoch < snap.Epoch:
			// The durable adapter appends the snapshot epoch point at EndOffset.
		default:
			return machineResult{Err: channel.ErrCorruptState}
		}
	}

	effectID := r.nextLoopEffectID()
	r.pendingSnapshotEffectID = effectID
	checkpoint := channel.Checkpoint{
		Epoch:          snap.Epoch,
		LogStartOffset: snap.EndOffset,
		HW:             snap.EndOffset,
	}
	snapshot := snap
	snapshot.Payload = append([]byte(nil), snap.Payload...)
	return machineResult{Effects: []machineEffect{installSnapshotEffect{
		EffectID:       effectID,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		RoleGeneration: r.roleGeneration,
		Snapshot:       snapshot,
		Checkpoint:     checkpoint,
		EpochPoint:     epochPoint,
	}}}
}

func (r *replica) executeInstallSnapshotEffect(ctx context.Context, effect installSnapshotEffect) error {
	r.mu.Lock()
	if err := r.validateInstallSnapshotEffectFenceLocked(effect); err != nil {
		r.mu.Unlock()
		result := r.submitLoopCommand(context.Background(), machineSnapshotInstalledEvent{
			EffectID:       effect.EffectID,
			ChannelKey:     effect.ChannelKey,
			Epoch:          effect.Epoch,
			RoleGeneration: effect.RoleGeneration,
			Err:            err,
		})
		if result.Err != nil {
			return result.Err
		}
		return err
	}
	r.mu.Unlock()

	var (
		view      durableView
		err       error
		committed bool
	)
	if err = r.lockDurableMu(ctx); err == nil {
		r.mu.Lock()
		err = r.validateInstallSnapshotEffectFenceLocked(effect)
		r.mu.Unlock()
		if err == nil {
			_, err = r.durable.InstallSnapshotAtomically(ctx, effect.Snapshot, effect.Checkpoint, effect.EpochPoint)
		}
		if err == nil {
			committed = true
			view, err = r.durable.Recover(context.Background())
		}
		r.durableMu.Unlock()
	}

	result := r.submitLoopCommand(context.Background(), machineSnapshotInstalledEvent{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		RoleGeneration: effect.RoleGeneration,
		View:           view,
		Committed:      committed,
		Err:            err,
	})
	return result.Err
}

func (r *replica) validateInstallSnapshotEffectFenceLocked(effect installSnapshotEffect) error {
	if r.closed {
		return channel.ErrNotLeader
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if r.pendingSnapshotEffectID == 0 || effect.EffectID != r.pendingSnapshotEffectID {
		return channel.ErrStaleMeta
	}
	if effect.ChannelKey != r.state.ChannelKey || effect.Epoch != r.state.Epoch || effect.RoleGeneration != r.roleGeneration {
		return channel.ErrStaleMeta
	}
	return nil
}

func (r *replica) applySnapshotInstalledEvent(ev machineSnapshotInstalledEvent) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.applySnapshotInstalledLocked(ev)
}

func (r *replica) applySnapshotInstalledLocked(ev machineSnapshotInstalledEvent) machineResult {
	if r.pendingSnapshotEffectID == 0 || ev.EffectID != r.pendingSnapshotEffectID {
		return machineResult{}
	}
	r.pendingSnapshotEffectID = 0
	if ev.Err != nil {
		return machineResult{Err: ev.Err}
	}
	if r.closed || r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{}
	}
	if ev.ChannelKey != r.state.ChannelKey || ev.Epoch != r.state.Epoch || ev.RoleGeneration != r.roleGeneration {
		return machineResult{}
	}

	checkpoint := ev.View.Checkpoint
	previous := r.state
	nextHistory := append([]channel.EpochPoint(nil), ev.View.EpochHistory...)
	nextState := r.state
	nextState.Role = channel.ReplicaRoleFollower
	nextState.Epoch = checkpoint.Epoch
	nextState.OffsetEpoch = offsetEpochForLEO(nextHistory, ev.View.LEO)
	nextState.LogStartOffset = checkpoint.LogStartOffset
	nextState.HW = checkpoint.HW
	nextState.CheckpointHW = checkpoint.HW
	nextState.CommitReady = true
	nextState.LEO = ev.View.LEO
	if err := checkReplicaInvariant(replicaInvariantCheck{
		State:             nextState,
		EpochHistory:      nextHistory,
		PreviousState:     &previous,
		SnapshotEndOffset: &checkpoint.LogStartOffset,
	}); err != nil {
		return machineResult{Err: err}
	}
	r.epochHistory = nextHistory
	r.state = nextState
	r.reconcilePending = nil
	r.pendingLeaderEpochEffectID = 0
	r.pendingReconcileEffectID = 0
	r.failOutstandingAppendWorkLocked(channel.ErrNotLeader)
	r.roleGeneration++
	r.publishStateLocked()
	return machineResult{}
}

func (r *replica) nextLoopEffectID() uint64 {
	r.nextEffectID++
	return r.nextEffectID
}
