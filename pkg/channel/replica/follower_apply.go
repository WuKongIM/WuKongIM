package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) ApplyFetch(ctx context.Context, req channel.ReplicaApplyFetchRequest) error {
	result := r.submitLoopCommand(ctx, machineApplyFetchCommand{Request: req})
	if result.Err != nil {
		return result.Err
	}
	for _, effect := range result.Effects {
		apply, ok := effect.(applyFollowerEffect)
		if !ok {
			continue
		}
		return r.executeFollowerApplyEffect(ctx, apply)
	}
	return nil
}

func (r *replica) applyFetchCommand(cmd machineApplyFetchCommand) machineResult {
	req := cmd.Request

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	if r.pendingFollowerApplyEffectID != 0 {
		return machineResult{Err: channel.ErrNotReady}
	}
	if r.state.Role != channel.ReplicaRoleFollower && r.state.Role != channel.ReplicaRoleFencedLeader {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if r.state.ChannelKey != "" && req.ChannelKey != r.state.ChannelKey {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if req.Epoch != r.state.Epoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if r.state.Leader != 0 && req.Leader != r.state.Leader {
		return machineResult{Err: channel.ErrStaleMeta}
	}

	baseLEO := r.state.LEO
	if req.TruncateTo != nil {
		truncateTo := *req.TruncateTo
		if truncateTo < r.state.HW || truncateTo > baseLEO {
			return machineResult{Err: channel.ErrCorruptState}
		}
		baseLEO = truncateTo
	}

	if len(req.Records) > 0 {
		return r.prepareFollowerRecordApplyLocked(req, baseLEO)
	}
	return r.prepareFollowerHeartbeatApplyLocked(req, baseLEO)
}

func (r *replica) prepareFollowerRecordApplyLocked(req channel.ReplicaApplyFetchRequest, baseLEO uint64) machineResult {
	if err := validateFetchedRecordIndexes(req.Records, baseLEO+1); err != nil {
		return machineResult{Err: err}
	}

	var epochPoint *channel.EpochPoint
	if len(r.epochHistory) == 0 || r.epochHistory[len(r.epochHistory)-1].Epoch != req.Epoch {
		point := channel.EpochPoint{Epoch: req.Epoch, StartOffset: baseLEO}
		if _, err := appendEpochPointInMemory(r.epochHistory, point); err != nil {
			return machineResult{Err: err}
		}
		epochPoint = &point
	}

	nextLEO := baseLEO + uint64(len(req.Records))
	nextHW := req.LeaderHW
	if nextHW > nextLEO {
		nextHW = nextLEO
	}
	previousHW := r.state.HW
	if nextHW < previousHW {
		return machineResult{Err: channel.ErrCorruptState}
	}

	var checkpoint *channel.Checkpoint
	if nextHW > previousHW {
		value := channel.Checkpoint{
			Epoch:          req.Epoch,
			LogStartOffset: r.state.LogStartOffset,
			HW:             nextHW,
		}
		checkpoint = &value
	}

	effect := r.newFollowerApplyEffectLocked(req, baseLEO, nextLEO, previousHW, nextHW, true)
	effect.Records = cloneRecords(req.Records)
	effect.Checkpoint = cloneCheckpointPointer(checkpoint)
	effect.EpochPoint = cloneEpochPointPointer(epochPoint)
	return machineResult{Effects: []machineEffect{effect}}
}

func (r *replica) prepareFollowerHeartbeatApplyLocked(req channel.ReplicaApplyFetchRequest, baseLEO uint64) machineResult {
	nextHW := req.LeaderHW
	if nextHW > baseLEO {
		nextHW = baseLEO
	}
	if nextHW < r.state.HW {
		if req.TruncateTo == nil {
			r.state.LEO = baseLEO
			r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, baseLEO)
			r.publishStateLocked()
			return machineResult{}
		}
		return machineResult{Err: channel.ErrCorruptState}
	}
	if nextHW == r.state.HW && req.TruncateTo == nil {
		r.state.LEO = baseLEO
		r.state.CommitReady = true
		r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, baseLEO)
		r.publishStateLocked()
		return machineResult{}
	}

	var checkpoint *channel.Checkpoint
	if nextHW > r.state.HW {
		value := channel.Checkpoint{
			Epoch:          req.Epoch,
			LogStartOffset: r.state.LogStartOffset,
			HW:             nextHW,
		}
		checkpoint = &value
	}
	effect := r.newFollowerApplyEffectLocked(req, baseLEO, baseLEO, r.state.HW, nextHW, true)
	effect.Checkpoint = cloneCheckpointPointer(checkpoint)
	return machineResult{Effects: []machineEffect{effect}}
}

func (r *replica) newFollowerApplyEffectLocked(req channel.ReplicaApplyFetchRequest, baseLEO, newLEO, previousHW, newHW uint64, commitReady bool) applyFollowerEffect {
	effectID := r.nextLoopEffectID()
	r.pendingFollowerApplyEffectID = effectID
	effect := applyFollowerEffect{
		EffectID:       effectID,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		Leader:         r.state.Leader,
		RoleGeneration: r.roleGeneration,
		BaseLEO:        baseLEO,
		NewLEO:         newLEO,
		PreviousHW:     previousHW,
		NewHW:          newHW,
		CommitReady:    commitReady,
		StartedAt:      r.now(),
	}
	if req.TruncateTo != nil {
		truncateTo := *req.TruncateTo
		effect.TruncateTo = &truncateTo
	}
	return effect
}

func (r *replica) executeFollowerApplyEffect(ctx context.Context, effect applyFollowerEffect) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var (
		storedLEO = effect.BaseLEO
		truncated bool
		err       error
	)
	if err = r.lockDurableMu(ctx); err == nil {
		r.mu.Lock()
		err = r.validateFollowerApplyEffectFenceLocked(effect)
		r.mu.Unlock()
		if err == nil && effect.TruncateTo != nil {
			err = r.durable.TruncateLogAndHistory(ctx, *effect.TruncateTo)
			truncated = err == nil
		}
		if err == nil && len(effect.Records) > 0 {
			applyReq := channel.ApplyFetchStoreRequest{
				PreviousCommittedHW: effect.PreviousHW,
				Records:             cloneRecords(effect.Records),
				Checkpoint:          cloneCheckpointPointer(effect.Checkpoint),
			}
			storedLEO, err = r.durable.ApplyFollowerBatch(ctx, applyReq, cloneEpochPointPointer(effect.EpochPoint))
		} else if err == nil && effect.Checkpoint != nil {
			err = r.durable.StoreCheckpointMonotonic(ctx, *effect.Checkpoint, effect.NewHW, effect.NewLEO)
		}
		r.durableMu.Unlock()
	}

	finishedAt := r.now()
	if err == nil && storedLEO != effect.NewLEO {
		err = channel.ErrCorruptState
	}
	result := r.submitLoopCommand(context.Background(), machineFollowerApplyResultCommand{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		Leader:         effect.Leader,
		RoleGeneration: effect.RoleGeneration,
		StoredLEO:      storedLEO,
		NewHW:          effect.NewHW,
		CommitReady:    effect.CommitReady,
		Truncated:      truncated,
		TruncateTo:     cloneUint64Pointer(effect.TruncateTo),
		Checkpoint:     cloneCheckpointPointer(effect.Checkpoint),
		EpochPoint:     cloneEpochPointPointer(effect.EpochPoint),
		Err:            err,
	})
	if result.Err == nil && len(effect.Records) > 0 {
		sendtrace.Record(sendtrace.Event{
			Stage:      sendtrace.StageReplicaFollowerApplyDurable,
			At:         effect.StartedAt,
			Duration:   sendtrace.Elapsed(effect.StartedAt, finishedAt),
			NodeID:     uint64(r.localNode),
			PeerNodeID: uint64(effect.Leader),
			ChannelKey: string(effect.ChannelKey),
			RangeStart: effect.BaseLEO + 1,
			RangeEnd:   effect.NewLEO,
		})
	}
	return result.Err
}

func (r *replica) validateFollowerApplyEffectFenceLocked(effect applyFollowerEffect) error {
	if r.pendingFollowerApplyEffectID == 0 || effect.EffectID != r.pendingFollowerApplyEffectID {
		return channel.ErrStaleMeta
	}
	if r.closed {
		return channel.ErrNotLeader
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if r.state.Role != channel.ReplicaRoleFollower && r.state.Role != channel.ReplicaRoleFencedLeader {
		return channel.ErrNotLeader
	}
	if effect.ChannelKey != r.state.ChannelKey || effect.Epoch != r.state.Epoch ||
		effect.Leader != r.state.Leader || effect.RoleGeneration != r.roleGeneration {
		return channel.ErrStaleMeta
	}
	return nil
}

func (r *replica) applyFollowerApplyResultCommand(cmd machineFollowerApplyResultCommand) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.pendingFollowerApplyEffectID == 0 || cmd.EffectID != r.pendingFollowerApplyEffectID {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	r.pendingFollowerApplyEffectID = 0
	if r.closed {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	// A committed durable truncate must be reflected before stale term results
	// return, otherwise runtime state can remain ahead of the local log.
	if err := r.applyDurableTruncateResultLocked(cmd); err != nil {
		return machineResult{Err: err}
	}
	if cmd.ChannelKey != r.state.ChannelKey || cmd.Epoch != r.state.Epoch ||
		cmd.Leader != r.state.Leader || cmd.RoleGeneration != r.roleGeneration {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if cmd.Err != nil {
		return machineResult{Err: cmd.Err}
	}

	// Reconstruct the intended state from the durable result and current HW.
	if cmd.StoredLEO < r.state.HW {
		return machineResult{Err: channel.ErrCorruptState}
	}
	if cmd.EpochPoint != nil {
		nextHistory, err := appendEpochPointInMemory(r.epochHistory, *cmd.EpochPoint)
		if err != nil {
			return machineResult{Err: err}
		}
		r.epochHistory = nextHistory
	}
	r.state.LEO = cmd.StoredLEO
	if cmd.NewHW > cmd.StoredLEO {
		return machineResult{Err: channel.ErrCorruptState}
	}
	if cmd.NewHW < r.state.HW {
		return machineResult{Err: channel.ErrCorruptState}
	}
	r.state.HW = cmd.NewHW
	if cmd.Checkpoint != nil {
		r.state.CheckpointHW = cmd.Checkpoint.HW
	}
	r.state.CommitReady = cmd.CommitReady
	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, cmd.StoredLEO)
	r.publishStateLocked()
	return machineResult{}
}

func (r *replica) applyDurableTruncateResultLocked(cmd machineFollowerApplyResultCommand) error {
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
	r.publishStateLocked()
	return nil
}

func cloneUint64Pointer(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneCheckpointPointer(checkpoint *channel.Checkpoint) *channel.Checkpoint {
	if checkpoint == nil {
		return nil
	}
	value := *checkpoint
	return &value
}

func cloneEpochPointPointer(point *channel.EpochPoint) *channel.EpochPoint {
	if point == nil {
		return nil
	}
	value := *point
	return &value
}
