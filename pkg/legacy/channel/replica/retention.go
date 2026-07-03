package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

const retentionReplayCursorName = "committed"

// ApplyRetentionBoundary applies an authoritative logical retention fence and
// performs any durable local adoption or physical trim that is currently safe.
func (r *replica) ApplyRetentionBoundary(ctx context.Context, throughSeq uint64) error {
	if throughSeq == 0 {
		return nil
	}
	result := r.submitLoopCommand(ctx, machineApplyRetentionCommand{ThroughSeq: throughSeq})
	if result.Err != nil {
		return result.Err
	}
	return r.executeRetentionEffects(ctx, result.Effects)
}

// RetentionView returns a point-in-time retention progress view for planning.
func (r *replica) RetentionView() (channel.RetentionView, error) {
	if r == nil {
		return channel.RetentionView{}, channel.ErrInvalidConfig
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return channel.RetentionView{
		ChannelKey:                  r.state.ChannelKey,
		Epoch:                       r.state.Epoch,
		LeaderEpoch:                 r.meta.LeaderEpoch,
		Leader:                      r.state.Leader,
		LeaseUntil:                  r.meta.LeaseUntil,
		HW:                          r.state.HW,
		CheckpointHW:                r.state.CheckpointHW,
		LEO:                         r.state.LEO,
		CommitReady:                 r.state.CommitReady,
		RetentionThroughSeq:         r.state.RetentionThroughSeq,
		MinAvailableSeq:             channel.EffectiveMinAvailableSeq(r.state.RetentionThroughSeq, r.state.LogStartOffset),
		LocalRetentionThroughSeq:    r.state.LocalRetentionThroughSeq,
		PhysicalRetentionThroughSeq: r.state.PhysicalRetentionThroughSeq,
		MinISRMatchOffset:           r.minISRRetentionProgressLocked(),
	}, nil
}

func (r *replica) applyRetentionCommand(cmd machineApplyRetentionCommand) machineResult {
	if cmd.ThroughSeq == 0 {
		return machineResult{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}

	if cmd.ThroughSeq < r.state.RetentionThroughSeq {
		return machineResult{}
	}

	advanced := false
	if cmd.ThroughSeq > r.state.RetentionThroughSeq {
		r.state.RetentionThroughSeq = cmd.ThroughSeq
		advanced = true
	}

	var effects []machineEffect
	if cmd.ThroughSeq > r.state.LocalRetentionThroughSeq {
		effectID := r.nextLoopEffectID()
		r.pendingRetentionAdoptEffectID = effectID
		effects = append(effects, retentionAdoptEffect{
			EffectID:       effectID,
			ChannelKey:     r.state.ChannelKey,
			Epoch:          r.state.Epoch,
			RoleGeneration: r.roleGeneration,
			ThroughSeq:     cmd.ThroughSeq,
		})
	}
	if r.shouldTrimRetentionLocked(cmd.ThroughSeq) {
		effectID := r.nextLoopEffectID()
		r.pendingRetentionTrimEffectID = effectID
		effects = append(effects, retentionTrimEffect{
			EffectID:       effectID,
			ChannelKey:     r.state.ChannelKey,
			Epoch:          r.state.Epoch,
			RoleGeneration: r.roleGeneration,
			ThroughSeq:     cmd.ThroughSeq,
		})
	}
	if advanced {
		r.publishStateLocked()
	}
	return machineResult{Effects: effects}
}

func (r *replica) shouldTrimRetentionLocked(throughSeq uint64) bool {
	if throughSeq == 0 || throughSeq <= r.state.PhysicalRetentionThroughSeq {
		return false
	}
	return r.state.CommitReady &&
		throughSeq <= r.state.CheckpointHW &&
		throughSeq <= r.state.HW &&
		throughSeq <= r.state.LEO
}

func (r *replica) executeRetentionEffects(ctx context.Context, effects []machineEffect) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, effect := range effects {
		switch ev := effect.(type) {
		case retentionAdoptEffect:
			if err := r.executeRetentionAdoptEffect(ctx, ev); err != nil {
				return err
			}
		case retentionTrimEffect:
			if err := r.executeRetentionTrimEffect(ctx, ev); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *replica) executeRetentionAdoptEffect(ctx context.Context, effect retentionAdoptEffect) error {
	fenceErr := r.validateRetentionAdoptEffectFence(effect)
	storedLEO := uint64(0)
	if fenceErr == nil {
		if err := r.lockDurableMu(ctx); err != nil {
			fenceErr = err
		} else {
			fenceErr = r.validateRetentionAdoptEffectFence(effect)
			if fenceErr == nil {
				storedLEO, fenceErr = r.durable.AdoptRetentionBoundary(ctx, effect.ThroughSeq, retentionReplayCursorName)
			}
			r.durableMu.Unlock()
		}
	}
	result := r.submitLoopCommand(context.Background(), machineRetentionAdoptedEvent{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		RoleGeneration: effect.RoleGeneration,
		ThroughSeq:     effect.ThroughSeq,
		StoredLEO:      storedLEO,
		Err:            fenceErr,
	})
	return result.Err
}

func (r *replica) executeRetentionTrimEffect(ctx context.Context, effect retentionTrimEffect) error {
	fenceErr := r.validateRetentionTrimEffectFence(effect)
	physicalThrough := uint64(0)
	if fenceErr == nil {
		if err := r.lockDurableMu(ctx); err != nil {
			fenceErr = err
		} else {
			fenceErr = r.validateRetentionTrimEffectFence(effect)
			if fenceErr == nil {
				physicalThrough, fenceErr = r.durable.TrimMessagesThrough(ctx, effect.ThroughSeq)
			}
			r.durableMu.Unlock()
		}
	}
	result := r.submitLoopCommand(context.Background(), machineRetentionTrimmedEvent{
		EffectID:           effect.EffectID,
		ChannelKey:         effect.ChannelKey,
		Epoch:              effect.Epoch,
		RoleGeneration:     effect.RoleGeneration,
		ThroughSeq:         effect.ThroughSeq,
		PhysicalThroughSeq: physicalThrough,
		Err:                fenceErr,
	})
	return result.Err
}

func (r *replica) validateRetentionAdoptEffectFence(effect retentionAdoptEffect) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.validateRetentionEffectFenceLocked(effect.ChannelKey, effect.Epoch, effect.RoleGeneration, effect.EffectID, r.pendingRetentionAdoptEffectID)
}

func (r *replica) validateRetentionTrimEffectFence(effect retentionTrimEffect) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.validateRetentionEffectFenceLocked(effect.ChannelKey, effect.Epoch, effect.RoleGeneration, effect.EffectID, r.pendingRetentionTrimEffectID)
}

func (r *replica) validateRetentionEffectFenceLocked(channelKey channel.ChannelKey, epoch uint64, roleGeneration uint64, effectID uint64, pendingEffectID uint64) error {
	if pendingEffectID == 0 || effectID != pendingEffectID {
		return channel.ErrStaleMeta
	}
	if r.closed {
		return channel.ErrNotLeader
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if channelKey != r.state.ChannelKey || epoch != r.state.Epoch || roleGeneration != r.roleGeneration {
		return channel.ErrStaleMeta
	}
	return nil
}

func (r *replica) applyRetentionAdoptedEvent(ev machineRetentionAdoptedEvent) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.ChannelKey != r.state.ChannelKey || ev.Epoch != r.state.Epoch || ev.RoleGeneration != r.roleGeneration {
		return machineResult{}
	}
	if r.pendingRetentionAdoptEffectID == 0 || ev.EffectID != r.pendingRetentionAdoptEffectID {
		return machineResult{}
	}
	r.pendingRetentionAdoptEffectID = 0
	if ev.Err != nil {
		return machineResult{Err: ev.Err}
	}
	if ev.StoredLEO < ev.ThroughSeq {
		return machineResult{Err: channel.ErrCorruptState}
	}

	changed := false
	if ev.ThroughSeq > r.state.LocalRetentionThroughSeq {
		r.state.LocalRetentionThroughSeq = ev.ThroughSeq
		changed = true
	}
	if ev.StoredLEO > r.state.LEO {
		r.state.LEO = ev.StoredLEO
		r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, r.state.LEO)
		r.state.CommitReady = false
		changed = true
	}
	if changed {
		r.publishStateLocked()
	}
	return machineResult{}
}

func (r *replica) applyRetentionTrimmedEvent(ev machineRetentionTrimmedEvent) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.ChannelKey != r.state.ChannelKey || ev.Epoch != r.state.Epoch || ev.RoleGeneration != r.roleGeneration {
		return machineResult{}
	}
	if r.pendingRetentionTrimEffectID == 0 || ev.EffectID != r.pendingRetentionTrimEffectID {
		return machineResult{}
	}
	r.pendingRetentionTrimEffectID = 0
	if ev.Err != nil {
		return machineResult{Err: ev.Err}
	}
	if ev.PhysicalThroughSeq > r.state.LocalRetentionThroughSeq {
		return machineResult{Err: channel.ErrCorruptState}
	}
	if ev.PhysicalThroughSeq <= r.state.PhysicalRetentionThroughSeq {
		return machineResult{}
	}
	r.state.PhysicalRetentionThroughSeq = ev.PhysicalThroughSeq
	r.publishStateLocked()
	return machineResult{}
}

func (r *replica) clearRetentionEffectFencesLocked() {
	r.pendingRetentionAdoptEffectID = 0
	r.pendingRetentionTrimEffectID = 0
}

func retainedThroughOffset(state channel.ReplicaState) uint64 {
	if state.RetentionThroughSeq > state.LogStartOffset {
		return state.RetentionThroughSeq
	}
	return state.LogStartOffset
}

func retentionDominatesRetainedFloor(state channel.ReplicaState) bool {
	return state.RetentionThroughSeq > 0 && state.RetentionThroughSeq >= state.LogStartOffset
}

func retentionResetDominatesFloor(state channel.ReplicaState, fetchOffset uint64) bool {
	if state.RetentionThroughSeq == 0 {
		return false
	}
	return fetchOffset < retainedThroughOffset(state) &&
		retentionDominatesRetainedFloor(state)
}

func (r *replica) minISRRetentionProgressLocked() uint64 {
	if len(r.meta.ISR) == 0 {
		return r.state.RetentionThroughSeq
	}
	min := ^uint64(0)
	for _, id := range r.meta.ISR {
		match := r.state.RetentionThroughSeq
		if id == r.localNode {
			match = r.state.LEO
		} else if r.retentionProgress != nil {
			if observed, ok := r.retentionProgress[id]; ok {
				match = observed
			}
		}
		if match < r.state.RetentionThroughSeq {
			match = r.state.RetentionThroughSeq
		}
		if match < min {
			min = match
		}
	}
	if min == ^uint64(0) {
		return r.state.RetentionThroughSeq
	}
	return min
}

func retentionResetResult(state channel.ReplicaState) channel.ReplicaFetchResult {
	minAvailable := channel.EffectiveMinAvailableSeq(state.RetentionThroughSeq, state.LogStartOffset)
	return channel.ReplicaFetchResult{
		Epoch: state.Epoch,
		HW:    visibleCommittedHW(state),
		RetentionReset: &channel.RetentionReset{
			RetentionThroughSeq:   state.RetentionThroughSeq,
			RetainedThroughOffset: retainedThroughOffset(state),
			MinAvailableSeq:       minAvailable,
		},
	}
}
