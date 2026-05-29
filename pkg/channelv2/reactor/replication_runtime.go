package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) handleStoreCheckpointResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch {
		if rc.lifecycle.CheckpointInflight && result.Fence.OpID == rc.lifecycle.CheckpointOpID {
			resetLeaderCheckpointLifecycle(rc)
		}
		return
	}
	if rc.lifecycle.CheckpointInflight && result.Fence.OpID == rc.lifecycle.CheckpointOpID {
		r.handleLeaderCheckpointResult(rc, result)
		return
	}
	if !rc.replication.stopping || !rc.replication.checkpointInflight || result.Fence.OpID != rc.replication.checkpointOpID {
		return
	}
	rc.replication.checkpointInflight = false
	rc.replication.checkpointOpID = 0
	now := time.Now()
	err = result.Err
	if err == nil && result.StoreCheckpoint == nil {
		err = ch.ErrInvalidConfig
	}
	if err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.nextCheckpointAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = err
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.backoff = 0
	rc.replication.nextCheckpointAt = time.Time{}
	rc.replication.lastError = nil
	rc.runtimeLifecycle.FollowerPhase = FollowerLifecycleStopAcking
	r.submitAckPayload(rc, rc.state.LEO, true, rc.replication.stopActivityVersion, now)
	r.scheduleReplicationFromState(rc, now)
}

func (r *Reactor) handleLeaderCheckpointResult(rc *runtimeChannel, result worker.Result) {
	if rc == nil || rc.state == nil || !rc.lifecycle.CheckpointInflight || result.Fence.OpID != rc.lifecycle.CheckpointOpID {
		return
	}
	activityVersion := rc.lifecycle.CheckpointActivityVersion
	now := time.Now()
	rc.lifecycle.CheckpointInflight = false
	rc.lifecycle.CheckpointOpID = 0
	rc.lifecycle.CheckpointActivityVersion = 0
	rc.lifecycle.CheckpointReady = false
	rc.lifecycle.CheckpointReadyActivityVersion = 0
	rc.lifecycle.CheckpointReadyQueued = false
	err := result.Err
	if err == nil && result.StoreCheckpoint == nil {
		err = ch.ErrInvalidConfig
	}
	if err != nil {
		rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleCheckpointing
		rc.lifecycle.CheckpointRetryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.lifecycle.CheckpointRetryAt = time.Time{}
	if activityVersion != rc.lifecycle.ActivityVersion ||
		rc.state.Role != ch.RoleLeader ||
		rc.state.HW < rc.state.LEO ||
		!r.allFollowersStopped(rc) ||
		r.hasPendingRuntimeWork(rc) {
		r.scheduleLifecycleFromState(rc, now)
		return
	}
	rc.runtimeLifecycle.LeaderPhase = LeaderLifecycleFinalRecheck
	rc.lifecycle.CheckpointReady = true
	rc.lifecycle.CheckpointReadyActivityVersion = activityVersion
	r.submitLeaderEvictReady(rc, now, r.currentAppendSubmitSeq(rc.state.Key))
}

func (r *Reactor) nextOpID() ch.OpID {
	if r.cfg.NextOpID != nil {
		return r.cfg.NextOpID()
	}
	return ch.OpID(r.nextOp.Add(1))
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}
