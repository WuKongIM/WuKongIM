package replica

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) startCheckpointEffectWorker() {
	go func() {
		defer close(r.checkpointWorkerDone)
		for {
			select {
			case effect := <-r.checkpointEffects:
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() {
					done <- r.storeCheckpointEffect(ctx, effect)
				}()
				select {
				case err := <-done:
					cancel()
					_ = r.submitLoopResult(context.Background(), machineCheckpointStoredEvent{
						EffectID:       effect.EffectID,
						ChannelKey:     effect.ChannelKey,
						Epoch:          effect.Epoch,
						LeaderEpoch:    effect.LeaderEpoch,
						RoleGeneration: effect.RoleGeneration,
						Checkpoint:     effect.Checkpoint,
						Err:            err,
					})
				case <-r.stopCh:
					cancel()
					return
				}
			case <-r.stopCh:
				return
			}
		}
	}()
}

func (r *replica) storeCheckpointEffect(ctx context.Context, effect storeCheckpointEffect) error {
	if effect.Checkpoint.HW > effect.VisibleHW || effect.Checkpoint.HW > effect.LEO {
		return channel.ErrCorruptState
	}
	if err := r.lockDurableMu(ctx); err != nil {
		return err
	}
	defer r.durableMu.Unlock()
	r.mu.Lock()
	if err := r.validateCheckpointEffectFenceLocked(effect); err != nil {
		r.mu.Unlock()
		return err
	}
	r.mu.Unlock()
	if r.durable != nil {
		return r.durable.StoreCheckpointMonotonic(ctx, effect.Checkpoint, effect.VisibleHW, effect.LEO)
	}
	return r.checkpoints.Store(effect.Checkpoint)
}

func (r *replica) validateCheckpointEffectFenceLocked(effect storeCheckpointEffect) error {
	if effect.EffectID == 0 || effect.EffectID != r.pendingCheckpointEffectID {
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
	if effect.Checkpoint.HW > r.state.HW || effect.Checkpoint.HW > r.state.LEO {
		return channel.ErrCorruptState
	}
	return nil
}

func (r *replica) lockDurableMu(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if r.durableMu.TryLock() {
			return nil
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *replica) emitCheckpointEffectLocked() {
	if r.checkpointEffects == nil {
		return
	}
	if r.closed || r.state.Role == channel.ReplicaRoleTombstoned {
		return
	}
	if !r.checkpointQueued || r.checkpointInFlight {
		return
	}
	if r.pendingCheckpoint.HW <= r.state.CheckpointHW {
		r.checkpointQueued = false
		return
	}

	r.nextEffectID++
	effectID := r.nextEffectID
	checkpoint := r.pendingCheckpoint
	r.checkpointQueued = false
	r.checkpointInFlight = true
	r.pendingCheckpointEffectID = effectID
	effect := storeCheckpointEffect{
		EffectID:       effectID,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
		Checkpoint:     checkpoint,
		VisibleHW:      r.state.HW,
		LEO:            r.state.LEO,
	}
	select {
	case r.checkpointEffects <- effect:
	default:
		r.checkpointQueued = true
		r.checkpointInFlight = false
		r.pendingCheckpointEffectID = 0
		r.scheduleCheckpointRetry()
	}
}

func (r *replica) applyCheckpointStoredEvent(ev machineCheckpointStoredEvent) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if ev.EffectID == 0 || ev.EffectID != r.pendingCheckpointEffectID {
		return machineResult{}
	}
	r.checkpointInFlight = false
	r.pendingCheckpointEffectID = 0
	if r.closed || r.state.Role == channel.ReplicaRoleTombstoned {
		r.checkpointQueued = false
		return machineResult{}
	}
	if ev.ChannelKey != r.state.ChannelKey || ev.Epoch != r.state.Epoch ||
		ev.LeaderEpoch != r.meta.LeaderEpoch || ev.RoleGeneration != r.roleGeneration {
		r.checkpointQueued = r.hasPendingCheckpointLocked()
		r.emitCheckpointEffectLocked()
		return machineResult{}
	}

	if ev.Err != nil {
		r.checkpointQueued = r.hasPendingCheckpointLocked()
		r.state.CommitReady = false
		r.publishStateLocked()
		if r.checkpointQueued {
			r.scheduleCheckpointRetry()
		}
		return machineResult{}
	}
	if ev.Checkpoint.HW > r.state.HW || ev.Checkpoint.HW > r.state.LEO {
		return machineResult{Err: channel.ErrCorruptState}
	}
	if ev.Checkpoint.HW > r.state.CheckpointHW {
		r.state.CheckpointHW = ev.Checkpoint.HW
	}
	r.checkpointQueued = r.hasPendingCheckpointLocked()
	if !r.state.CommitReady && len(r.reconcilePending) == 0 && !r.checkpointQueued && r.state.CheckpointHW >= r.state.HW {
		r.state.CommitReady = true
	}
	r.publishStateLocked()
	r.emitCheckpointEffectLocked()
	return machineResult{}
}

func (r *replica) hasPendingCheckpointLocked() bool {
	return r.pendingCheckpoint.Epoch == r.state.Epoch && r.pendingCheckpoint.HW > r.state.CheckpointHW
}

func (r *replica) applyCheckpointRetryEvent() machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emitCheckpointEffectLocked()
	return machineResult{}
}
