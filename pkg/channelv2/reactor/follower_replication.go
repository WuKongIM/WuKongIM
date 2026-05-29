package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) tickFollowerReplication(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive {
		return
	}
	defer r.scheduleReplicationFromState(rc, now)
	if rc.replication.pendingAck {
		r.trySubmitPendingAck(rc, now)
		if rc.replication.pendingAck || rc.replication.ackInflight {
			return
		}
	}
	if rc.replication.ackInflight {
		return
	}
	if rc.lifecycle.stage == lifecycleFollowerReadyToEvict {
		r.tryEvictStoppedFollower(rc, now)
		return
	}
	if rc.lifecycle.followerStop.accepted {
		if rc.lifecycle.checkpoint.inflight || rc.lifecycle.stoppedAck.inflight {
			return
		}
		if rc.lifecycle.stage == lifecycleFollowerCheckpointing {
			r.trySubmitStopCheckpoint(rc, now)
			return
		}
		if rc.lifecycle.stage == lifecycleFollowerStoppedAcking {
			r.trySubmitStoppedAck(rc, now)
			return
		}
	}
	if rc.replication.pendingPull != nil {
		r.trySubmitPendingApply(rc, now)
		return
	}
	if rc.replication.pullInflight || (!rc.replication.nextPullAt.IsZero() && now.Before(rc.replication.nextPullAt)) {
		return
	}
	r.trySubmitPull(rc, now)
}

func (r *Reactor) trySubmitPull(rc *runtimeChannel, now time.Time) {
	if r.cfg.Pools == nil {
		r.backoffPull(rc, ch.ErrInvalidConfig, now)
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	req := transport.PullRequest{
		ChannelKey:  rc.state.Key,
		ChannelID:   rc.state.ID,
		Epoch:       rc.state.Epoch,
		LeaderEpoch: rc.state.LeaderEpoch,
		Follower:    r.cfg.LocalNode,
		NextOffset:  rc.state.LEO + 1,
		AckOffset:   rc.state.LEO,
		MaxBytes:    r.cfg.PullMaxBytes,
	}
	if err := r.submitRPCPull(context.Background(), rc.state.Leader, fence, req); err != nil {
		r.backoffPull(rc, err, now)
		return
	}
	rc.replication.pullInflight = true
	rc.replication.pullOpID = opID
	rc.replication.dirty = false
}

func (r *Reactor) trySubmitPendingApply(rc *runtimeChannel, now time.Time) {
	if rc.replication.pendingPull == nil || rc.replication.applyOpID != 0 {
		return
	}
	if rc.replication.ackInflight || rc.replication.pendingAck {
		rc.replication.applyBlocked = true
		return
	}
	if rc.replication.applyBlocked && !rc.replication.applyRetryAt.IsZero() && now.Before(rc.replication.applyRetryAt) {
		return
	}
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
	resp := rc.replication.pendingPull
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreApply(context.Background(), rc.state.ID, fence, resp.Records, resp.LeaderHW); err != nil {
		rc.replication.applyBlocked = true
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.applyRetryAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = err
		return
	}
	rc.replication.applyOpID = opID
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
}

func (r *Reactor) trySubmitPendingAck(rc *runtimeChannel, now time.Time) {
	if rc.replication.ackInflight || !rc.replication.pendingAck {
		return
	}
	if !rc.replication.nextAckAt.IsZero() && now.Before(rc.replication.nextAckAt) {
		return
	}
	r.submitAck(rc, rc.replication.pendingAckMatch, now)
}

func (r *Reactor) submitAck(rc *runtimeChannel, match uint64, now time.Time) bool {
	if match == 0 {
		return false
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	req := transport.AckRequest{ChannelKey: rc.state.Key, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, Follower: r.cfg.LocalNode, MatchOffset: match, Stopped: false}
	if err := r.submitRPCAck(context.Background(), rc.state.Leader, fence, req); err != nil {
		rc.replication.pendingAck = true
		rc.replication.pendingAckMatch = match
		rc.replication.ackInflight = false
		rc.replication.ackOpID = 0
		rc.replication.ackMatch = 0
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.nextAckAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = err
		return false
	}
	rc.replication.pendingAck = false
	rc.replication.pendingAckMatch = 0
	rc.replication.nextAckAt = time.Time{}
	rc.replication.ackInflight = true
	rc.replication.ackOpID = opID
	rc.replication.ackMatch = match
	return true
}

func (r *Reactor) trySubmitStopCheckpoint(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || !rc.lifecycle.followerStop.accepted || rc.lifecycle.checkpoint.inflight {
		return
	}
	if !rc.lifecycle.checkpoint.retryAt.IsZero() && now.Before(rc.lifecycle.checkpoint.retryAt) {
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreCheckpoint(context.Background(), rc.state.ID, fence, ch.Checkpoint{HW: rc.state.HW}); err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.checkpoint.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.checkpoint.version = rc.lifecycle.followerStop.version
		rc.replication.lastError = err
		return
	}
	rc.lifecycle.stage = lifecycleFollowerCheckpointing
	rc.lifecycle.checkpoint.inflight = true
	rc.lifecycle.checkpoint.opID = opID
	rc.lifecycle.checkpoint.version = rc.lifecycle.followerStop.version
	rc.lifecycle.checkpoint.retryAt = time.Time{}
}

func (r *Reactor) trySubmitStoppedAck(rc *runtimeChannel, now time.Time) bool {
	if rc == nil || rc.state == nil || !rc.lifecycle.followerStop.accepted || rc.lifecycle.stoppedAck.inflight {
		return false
	}
	if !rc.lifecycle.stoppedAck.retryAt.IsZero() && now.Before(rc.lifecycle.stoppedAck.retryAt) {
		return false
	}
	opID := r.nextOpID()
	version := rc.lifecycle.followerStop.version
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	req := transport.AckRequest{
		ChannelKey:      rc.state.Key,
		Epoch:           rc.state.Epoch,
		LeaderEpoch:     rc.state.LeaderEpoch,
		Follower:        r.cfg.LocalNode,
		MatchOffset:     rc.state.LEO,
		ActivityVersion: version,
		Stopped:         true,
	}
	if err := r.submitRPCAck(context.Background(), rc.state.Leader, fence, req); err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.stoppedAck.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.stoppedAck.version = version
		rc.replication.lastError = err
		return false
	}
	rc.lifecycle.stage = lifecycleFollowerStoppedAcking
	rc.lifecycle.stoppedAck.inflight = true
	rc.lifecycle.stoppedAck.opID = opID
	rc.lifecycle.stoppedAck.version = version
	rc.lifecycle.stoppedAck.retryAt = time.Time{}
	return true
}

// tryEvictStoppedFollower retries runtime deletion after the stopped ACK has already reached the leader.
func (r *Reactor) tryEvictStoppedFollower(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.lifecycle.stage != lifecycleFollowerReadyToEvict {
		return
	}
	if !rc.lifecycle.finalCheck.retryAt.IsZero() && now.Before(rc.lifecycle.finalCheck.retryAt) {
		return
	}
	rc.lifecycle.finalCheck.retryAt = time.Time{}
	if r.evictRuntimeChannel(rc.state.Key, rc, "stopped ack retry") {
		r.clearAppendSubmitState(rc.state.Key)
		return
	}
	rc.lifecycle.finalCheck.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
}

func (r *Reactor) handleFollowerPullHint(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	req := event.PullHint
	if req.ChannelKey != "" && req.ChannelKey != rc.state.Key {
		err = ch.ErrStaleMeta
	} else if req.ChannelID != (ch.ChannelID{}) && req.ChannelID != rc.state.ID {
		err = ch.ErrStaleMeta
	} else if rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive ||
		req.Epoch != rc.state.Epoch || req.LeaderEpoch != rc.state.LeaderEpoch ||
		req.Leader != rc.state.Leader || req.Leader == r.cfg.LocalNode ||
		!rc.state.IsReplica(r.cfg.LocalNode) {
		err = ch.ErrStaleMeta
	}
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{Err: err})
		}
		return
	}
	if req.ActivityVersion < rc.replication.lastActivityVersion {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	now := time.Now()
	if req.ActivityVersion > rc.replication.lastActivityVersion {
		rc.lifecycle.cancelFollowerStop()
		rc.replication.lastActivityVersion = req.ActivityVersion
	}
	if req.LeaderLEO <= rc.state.LEO {
		rc.replication.hintedLeaderLEO = 0
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	if req.LeaderLEO > rc.replication.hintedLeaderLEO {
		rc.replication.hintedLeaderLEO = req.LeaderLEO
	}
	rc.replication.parked = false
	rc.replication.nextPullAfter = 0
	rc.replication.markDirty(now)
	r.tickFollowerReplication(rc, now)
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

// handleLegacyFollowerNotify accepts the legacy transport compatibility nudge
// and maps it to the current PullHint-driven follower resume path.
func (r *Reactor) handleLegacyFollowerNotify(event Event) {
	rc, err := r.lookup(event.Key)
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	req := event.Notify
	if rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive ||
		req.Epoch != rc.state.Epoch || req.LeaderEpoch != rc.state.LeaderEpoch ||
		req.Leader != rc.state.Leader {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	now := time.Now()
	rc.replication.markDirty(now)
	r.tickFollowerReplication(rc, now)
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) handleRPCPullResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	current := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: rc.replication.pullOpID}
	if !rc.replication.applyPullResult(result, current, time.Now()) {
		return
	}
	now := time.Now()
	defer r.scheduleReplicationFromState(rc, now)
	if result.Err != nil {
		r.backoffPull(rc, result.Err, now)
		return
	}
	if result.RPCPull == nil {
		r.backoffPull(rc, ch.ErrInvalidConfig, now)
		return
	}
	resp := result.RPCPull.Response
	if resp.ChannelKey != rc.state.Key || resp.Epoch != rc.state.Epoch || resp.LeaderEpoch != rc.state.LeaderEpoch {
		r.backoffPull(rc, ch.ErrStaleMeta, now)
		return
	}
	rc.replication.lastLeaderHW = resp.LeaderHW
	rc.replication.backoff = 0
	if resp.Control == transport.PullControlStop && resp.ActivityVersion < rc.replication.lastActivityVersion {
		rc.replication.markDirty(now)
		r.tickFollowerReplication(rc, now)
		return
	}
	if resp.ActivityVersion > rc.replication.lastActivityVersion {
		rc.replication.lastActivityVersion = resp.ActivityVersion
	}
	if resp.Control == transport.PullControlStop {
		r.handleFollowerStopControl(rc, resp, now)
		return
	}
	if len(resp.Records) == 0 {
		rc.state.HW = minUint64(rc.state.LEO, resp.LeaderHW)
		if rc.replication.hintedLeaderLEO <= rc.state.LEO {
			rc.replication.hintedLeaderLEO = 0
		}
		if resp.LeaderLEO > rc.state.LEO || rc.replication.hintedLeaderLEO > rc.state.LEO {
			r.scheduleEmptyLaggingPullRetry(rc, now)
			return
		}
		rc.replication.dirty = false
		delay := resp.NextPullAfter
		if delay <= 0 {
			delay = r.cfg.ReplicationIdlePollInterval
		}
		rc.replication.parked = delay > 0
		rc.replication.nextPullAfter = delay
		rc.replication.nextPullAt = now.Add(delay)
		return
	}
	rc.replication.parked = false
	rc.replication.nextPullAfter = 0
	rc.replication.pendingPull = &resp
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
	rc.replication.applyOpID = 0
	r.trySubmitPendingApply(rc, now)
}

func (r *Reactor) scheduleEmptyLaggingPullRetry(rc *runtimeChannel, now time.Time) {
	delay := r.cfg.ReplicationMinBackoff
	if delay <= 0 {
		delay = time.Millisecond
	}
	rc.replication.dirty = true
	rc.replication.parked = false
	rc.replication.nextPullAfter = delay
	rc.replication.nextPullAt = now.Add(delay)
}

func (r *Reactor) handleFollowerStopControl(rc *runtimeChannel, resp transport.PullResponse, now time.Time) {
	if rc == nil || rc.state == nil {
		return
	}
	view := runtimeViewFromChannel(rc, now, AppendFenceView{})
	lifecycle := runtimeLifecycleFromController(rc.lifecycle)
	decision := lifecycle.OnFollowerLifecycleEvent(FollowerLifecycleEvent{
		Kind:            FollowerLifecycleStopOffered,
		Now:             now,
		LeaderLEO:       resp.LeaderLEO,
		LeaderHW:        resp.LeaderHW,
		ActivityVersion: resp.ActivityVersion,
	}, view, r.lifecycleConfig())
	if !decisionHasAction(decision, LifecycleActionStartFollowerStopCheckpoint) {
		rc.lifecycle.cancelFollowerStop()
		rc.replication.markDirty(now)
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.state.HW = minUint64(rc.state.LEO, resp.LeaderHW)
	rc.lifecycle.acceptFollowerStop(resp.ActivityVersion, resp.LeaderLEO, resp.LeaderHW)
	rc.replication.parked = false
	rc.replication.dirty = false
	rc.replication.nextPullAt = time.Time{}
	rc.replication.nextPullAfter = 0
	r.trySubmitStopCheckpoint(rc, now)
}

func (r *Reactor) handleStoreApplyResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch || result.Fence.OpID != rc.replication.applyOpID {
		return
	}
	rc.replication.applyOpID = 0
	now := time.Now()
	defer r.scheduleReplicationFromState(rc, now)
	if result.Err != nil {
		rc.replication.applyBlocked = true
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.applyRetryAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = result.Err
		return
	}
	if result.StoreApply == nil {
		rc.replication.applyBlocked = true
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.applyRetryAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = ch.ErrInvalidConfig
		return
	}
	rc.state.LEO = result.StoreApply.LEO
	rc.state.HW = minUint64(rc.state.LEO, rc.replication.lastLeaderHW)
	if rc.replication.hintedLeaderLEO <= rc.state.LEO {
		rc.replication.hintedLeaderLEO = 0
	}
	rc.replication.pendingPull = nil
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
	rc.replication.backoff = 0
	rc.replication.lastError = nil
	rc.replication.markDirty(now)
	rc.replication.nextPullAt = now
}

func (r *Reactor) handleRPCAckResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	if rc.lifecycle.stoppedAck.inflight &&
		result.Fence.ChannelKey == rc.state.Key &&
		result.Fence.Generation == rc.state.Generation &&
		result.Fence.Epoch == rc.state.Epoch &&
		result.Fence.LeaderEpoch == rc.state.LeaderEpoch &&
		result.Fence.OpID == rc.lifecycle.stoppedAck.opID {
		r.handleStoppedAckResult(rc, result)
		return
	}
	current := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: rc.replication.ackOpID}
	if !rc.replication.applyAckResult(result, current, time.Now()) {
		return
	}
	now := time.Now()
	if result.Err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.nextAckAt = now.Add(rc.replication.backoff)
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.backoff = 0
	rc.replication.dirty = false
	if rc.replication.pendingPull != nil {
		r.trySubmitPendingApply(rc, now)
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.nextPullAt = now
	r.scheduleReplicationFromState(rc, now)
}

func (r *Reactor) handleStoppedAckResult(rc *runtimeChannel, result worker.Result) {
	now := time.Now()
	version := rc.lifecycle.stoppedAck.version
	rc.lifecycle.stoppedAck.inflight = false
	rc.lifecycle.stoppedAck.opID = 0
	if result.Err != nil {
		if errors.Is(result.Err, ch.ErrStaleMeta) {
			rc.lifecycle.cancelFollowerStop()
			rc.replication.backoff = 0
			rc.replication.lastError = result.Err
			rc.replication.markDirty(now)
			r.scheduleReplicationFromState(rc, now)
			return
		}
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.lifecycle.stoppedAck.retryAt = now.Add(rc.replication.backoff)
		rc.lifecycle.stoppedAck.version = version
		rc.replication.lastError = result.Err
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.backoff = 0
	rc.replication.lastError = nil
	rc.lifecycle.stoppedAck.retryAt = time.Time{}
	rc.lifecycle.stage = lifecycleFollowerReadyToEvict
	if !r.evictRuntimeChannel(rc.state.Key, rc, "stopped ack") {
		rc.lifecycle.finalCheck.retryAt = now.Add(r.cfg.IdleEvictCheckInterval)
		r.scheduleReplicationFromState(rc, now)
		return
	}
	r.clearAppendSubmitState(rc.state.Key)
}

func (r *Reactor) backoffPull(rc *runtimeChannel, err error, now time.Time) {
	rc.replication.pullInflight = false
	rc.replication.pullOpID = 0
	rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
	rc.replication.nextPullAt = now.Add(rc.replication.backoff)
	rc.replication.lastError = err
}

func (rc *runtimeChannel) canAcceptFollowerStop() bool {
	if rc == nil {
		return false
	}
	replication := rc.replication
	return !replication.pullInflight &&
		!replication.ackInflight &&
		!replication.pendingAck &&
		replication.pendingPull == nil &&
		!replication.applyBlocked &&
		replication.applyOpID == 0 &&
		!rc.lifecycle.followerStop.accepted &&
		!rc.lifecycle.checkpoint.inflight &&
		!rc.lifecycle.stoppedAck.inflight
}
