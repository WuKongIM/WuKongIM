package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) applyLoopEvent(event machineEvent) machineResult {
	if _, closing := event.(machineCloseCommand); !closing {
		if _, command := event.(machineCommand); command && r.isClosed() {
			return machineResult{Err: channel.ErrNotLeader}
		}
	}
	switch ev := event.(type) {
	case machineAppendRequestCommand:
		return r.applyAppendRequestCommand(ev)
	case machineAppendCancelCommand:
		return r.applyAppendCancelCommand(ev)
	case machineAppendFlushEvent:
		return r.applyAppendFlushEvent()
	case machineLeaderAppendCommittedEvent:
		return r.applyLeaderAppendCommittedEvent(ev)
	case machineApplyMetaCommand:
		return r.applyMetaCommand(ev)
	case machineBecomeLeaderCommand:
		return r.applyBecomeLeaderCommand(ev)
	case machineBeginLeaderEpochResultCommand:
		return r.applyBeginLeaderEpochResultCommand(ev)
	case machineBecomeFollowerCommand:
		return r.applyBecomeFollowerCommand(ev)
	case machineTombstoneCommand:
		return r.applyTombstoneCommand()
	case machineCloseCommand:
		return r.applyCloseCommand()
	case machineInstallSnapshotCommand:
		return r.applyInstallSnapshotCommand(ev)
	case machineSnapshotInstalledEvent:
		return r.applySnapshotInstalledEvent(ev)
	case machineApplyFetchCommand:
		return r.applyFetchCommand(ev)
	case machineFollowerApplyResultCommand:
		return r.applyFollowerApplyResultCommand(ev)
	case machineCursorCommand:
		return r.applyCursorCommand(ev)
	case machineFetchProgressCommand:
		return r.applyFetchProgressCommand(ev)
	case machineReadLogResultCommand:
		return r.applyReadLogResultCommand(ev)
	case machineAdvanceHWEvent:
		return r.applyAdvanceHWEvent()
	case machineCheckpointStoredEvent:
		return r.applyCheckpointStoredEvent(ev)
	case machineCheckpointRetryEvent:
		return r.applyCheckpointRetryEvent()
	case machineReconcileProofCommand:
		return r.applyReconcileProofCommand(ev)
	case machineCompleteReconcileCommand:
		return r.applyCompleteReconcileCommand(ev)
	case machineLeaderReconcileResultCommand:
		return r.applyLeaderReconcileResultCommand(ev)
	default:
		return machineResult{Err: channel.ErrInvalidArgument}
	}
}

func (r *replica) applyMetaCommand(cmd machineApplyMetaCommand) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	if err := r.applyMetaLocked(cmd.Meta); err != nil {
		return machineResult{Err: err}
	}
	r.pendingLeaderEpochEffectID = 0
	r.pendingReconcileEffectID = 0
	r.roleGeneration++
	var effects []machineEffect
	if r.state.Role == channel.ReplicaRoleLeader || r.state.Role == channel.ReplicaRoleFencedLeader {
		r.beginLeaderReconcileLocked()
		if r.needsLeaderReconcileLocked() {
			needsLocalReconcile := len(r.reconcilePending) == 0
			needsReconcile := r.probeSource != nil && !needsLocalReconcile
			if needsLocalReconcile || needsReconcile {
				effects = append(effects, leaderReconcileEffect{
					Meta:       r.meta,
					Local:      needsLocalReconcile,
					Probe:      needsReconcile,
					ProbeStore: r.probeSource,
				})
			}
		}
	}
	r.publishStateLocked()
	return machineResult{Effects: effects}
}

func (r *replica) applyBecomeLeaderCommand(cmd machineBecomeLeaderCommand) machineResult {
	r.mu.Lock()
	if r.state.Role == channel.ReplicaRoleTombstoned {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrTombstoned}
	}
	if !r.recovered {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrCorruptState}
	}

	normalized, err := normalizeMeta(cmd.Meta)
	if err != nil {
		r.mu.Unlock()
		return machineResult{Err: err}
	}
	if normalized.Leader != r.localNode {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrInvalidMeta}
	}
	if err := r.validateMetaLocked(normalized); err != nil {
		r.mu.Unlock()
		return machineResult{Err: err}
	}

	leo := r.log.LEO()
	if leo < r.state.HW {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrCorruptState}
	}
	if !r.now().Before(normalized.LeaseUntil) {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrLeaseExpired}
	}

	if len(r.epochHistory) == 0 || r.epochHistory[len(r.epochHistory)-1].Epoch != normalized.Epoch {
		point := channel.EpochPoint{Epoch: normalized.Epoch, StartOffset: leo}
		if _, err := appendEpochPointInMemory(r.epochHistory, point); err != nil {
			r.mu.Unlock()
			return machineResult{Err: err}
		}
		if r.pendingLeaderEpochEffectID != 0 {
			r.mu.Unlock()
			return machineResult{Err: channel.ErrNotReady}
		}
		effectID := r.nextLoopEffectID()
		r.pendingLeaderEpochEffectID = effectID
		result := machineResult{Effects: []machineEffect{beginLeaderEpochEffect{
			EffectID:       effectID,
			Meta:           normalized,
			RoleGeneration: r.roleGeneration,
			LEO:            leo,
			EpochPoint:     point,
		}}}
		r.mu.Unlock()
		return result
	}

	result := r.finishBecomeLeaderLocked(normalized, leo)
	r.mu.Unlock()
	return result
}

func (r *replica) finishBecomeLeaderLocked(normalized channel.Meta, leo uint64) machineResult {
	r.commitMetaLocked(normalized)
	r.state.Role = channel.ReplicaRoleLeader
	r.state.LEO = leo
	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, leo)
	r.pendingLeaderEpochEffectID = 0
	r.pendingReconcileEffectID = 0
	r.seedLeaderProgressLocked(normalized.ISR, leo, r.state.HW)
	needsLeaderReconcile := r.needsLeaderReconcileLocked()
	r.beginLeaderReconcileLocked()
	needsLocalReconcile := needsLeaderReconcile && len(r.reconcilePending) == 0
	r.roleGeneration++
	r.publishStateLocked()
	probeSource := r.probeSource
	needsReconcile := needsLeaderReconcile && probeSource != nil

	if !needsLocalReconcile && !needsReconcile {
		return machineResult{}
	}
	return machineResult{Effects: []machineEffect{leaderReconcileEffect{
		Meta:       normalized,
		Local:      needsLocalReconcile,
		Probe:      needsReconcile,
		ProbeStore: probeSource,
	}}}
}

func (r *replica) applyBeginLeaderEpochResultCommand(cmd machineBeginLeaderEpochResultCommand) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pendingLeaderEpochEffectID == 0 || cmd.EffectID != r.pendingLeaderEpochEffectID {
		return machineResult{}
	}
	r.pendingLeaderEpochEffectID = 0
	if cmd.Err != nil {
		return machineResult{Err: cmd.Err}
	}
	if r.closed {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	if cmd.RoleGeneration != r.roleGeneration {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if !r.now().Before(cmd.Meta.LeaseUntil) {
		return machineResult{Err: channel.ErrLeaseExpired}
	}
	if err := r.validateMetaLocked(cmd.Meta); err != nil {
		return machineResult{Err: err}
	}
	if leo := r.log.LEO(); leo != cmd.LEO {
		return machineResult{Err: channel.ErrCorruptState}
	}
	nextHistory, err := appendEpochPointInMemory(r.epochHistory, cmd.EpochPoint)
	if err != nil {
		return machineResult{Err: err}
	}
	r.epochHistory = nextHistory
	return r.finishBecomeLeaderLocked(cmd.Meta, cmd.LEO)
}

func (r *replica) executeBeginLeaderEpochEffect(ctx context.Context, effect beginLeaderEpochEffect) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	fenceErr := r.validateBeginLeaderEpochEffectFenceLocked(effect)
	r.mu.Unlock()
	if fenceErr == nil {
		if err := r.lockDurableMu(ctx); err != nil {
			fenceErr = err
		} else {
			r.mu.Lock()
			fenceErr = r.validateBeginLeaderEpochEffectFenceLocked(effect)
			r.mu.Unlock()
			if fenceErr == nil {
				fenceErr = r.durable.BeginEpoch(ctx, effect.EpochPoint, effect.LEO)
			}
			r.durableMu.Unlock()
		}
	}

	result := r.submitLoopCommand(context.Background(), machineBeginLeaderEpochResultCommand{
		EffectID:       effect.EffectID,
		Meta:           effect.Meta,
		RoleGeneration: effect.RoleGeneration,
		LEO:            effect.LEO,
		EpochPoint:     effect.EpochPoint,
		Err:            fenceErr,
	})
	if result.Err != nil {
		return result.Err
	}
	return r.executeLeaderReconcileEffects(result.Effects)
}

func (r *replica) validateBeginLeaderEpochEffectFenceLocked(effect beginLeaderEpochEffect) error {
	if r.pendingLeaderEpochEffectID == 0 || effect.EffectID != r.pendingLeaderEpochEffectID {
		return channel.ErrStaleMeta
	}
	if r.closed {
		return channel.ErrNotLeader
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if effect.RoleGeneration != r.roleGeneration {
		return channel.ErrStaleMeta
	}
	if !r.now().Before(effect.Meta.LeaseUntil) {
		return channel.ErrLeaseExpired
	}
	if err := r.validateMetaLocked(effect.Meta); err != nil {
		return err
	}
	if leo := r.log.LEO(); leo != effect.LEO {
		return channel.ErrCorruptState
	}
	return nil
}

func (r *replica) applyBecomeFollowerCommand(cmd machineBecomeFollowerCommand) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	if cmd.Meta.Leader == r.localNode {
		return machineResult{Err: channel.ErrInvalidMeta}
	}
	if err := r.applyMetaLocked(cmd.Meta); err != nil {
		return machineResult{Err: err}
	}
	r.state.Role = channel.ReplicaRoleFollower
	r.reconcilePending = nil
	r.pendingLeaderEpochEffectID = 0
	r.pendingReconcileEffectID = 0
	r.failOutstandingAppendWorkLocked(channel.ErrNotLeader)
	r.roleGeneration++
	r.publishStateLocked()
	return machineResult{}
}

func (r *replica) applyTombstoneCommand() machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.state.Role = channel.ReplicaRoleTombstoned
	r.reconcilePending = nil
	r.pendingLeaderEpochEffectID = 0
	r.pendingReconcileEffectID = 0
	r.failOutstandingAppendWorkLocked(channel.ErrTombstoned)
	r.roleGeneration++
	r.publishStateLocked()
	return machineResult{}
}

func (r *replica) applyCloseCommand() machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
	r.pendingLeaderEpochEffectID = 0
	r.pendingReconcileEffectID = 0
	r.failOutstandingAppendWorkLocked(channel.ErrNotLeader)
	r.roleGeneration++
	r.publishStateLocked()
	return machineResult{}
}

func (r *replica) executeLeaderReconcileEffects(effects []machineEffect) error {
	for _, effect := range effects {
		switch reconcile := effect.(type) {
		case beginLeaderEpochEffect:
			return r.executeBeginLeaderEpochEffect(context.Background(), reconcile)
		case leaderReconcileEffect:
			if reconcile.Local {
				return r.runLocalLeaderReconcile(reconcile.Meta)
			}
			if reconcile.Probe && reconcile.ProbeStore != nil {
				return r.runConfiguredLeaderReconcile(context.Background(), reconcile.Meta, reconcile.ProbeStore)
			}
		case leaderReconcileDurableEffect:
			return r.executeLeaderReconcileDurableEffect(context.Background(), reconcile)
		}
	}
	return nil
}
