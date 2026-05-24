package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) tickReplication(rc *runtimeChannel, now time.Time) {
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
	if rc.replication.stopping {
		if rc.replication.stopAcked {
			r.tryEvictStoppedFollower(rc, now)
			return
		}
		if rc.replication.checkpointInflight {
			return
		}
		r.trySubmitStopCheckpoint(rc, now)
		return
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
	r.submitAckPayload(rc, rc.replication.pendingAckMatch, rc.replication.pendingAckStopped, rc.replication.pendingAckActivityVersion, now)
}

func (r *Reactor) submitAck(rc *runtimeChannel, match uint64, now time.Time) bool {
	return r.submitAckPayload(rc, match, false, 0, now)
}

func (r *Reactor) submitAckPayload(rc *runtimeChannel, match uint64, stopped bool, activityVersion uint64, now time.Time) bool {
	if match == 0 && !stopped {
		return false
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	req := transport.AckRequest{ChannelKey: rc.state.Key, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, Follower: r.cfg.LocalNode, MatchOffset: match, ActivityVersion: activityVersion, Stopped: stopped}
	if err := r.submitRPCAck(context.Background(), rc.state.Leader, fence, req); err != nil {
		rc.replication.pendingAck = true
		rc.replication.pendingAckMatch = match
		rc.replication.pendingAckStopped = stopped
		rc.replication.pendingAckActivityVersion = activityVersion
		rc.replication.ackInflight = false
		rc.replication.ackOpID = 0
		rc.replication.ackMatch = 0
		rc.replication.ackStopped = false
		rc.replication.ackActivityVersion = 0
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.nextAckAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = err
		return false
	}
	rc.replication.pendingAck = false
	rc.replication.pendingAckMatch = 0
	rc.replication.pendingAckStopped = false
	rc.replication.pendingAckActivityVersion = 0
	rc.replication.nextAckAt = time.Time{}
	rc.replication.ackInflight = true
	rc.replication.ackOpID = opID
	rc.replication.ackMatch = match
	rc.replication.ackStopped = stopped
	rc.replication.ackActivityVersion = activityVersion
	return true
}

func (r *Reactor) trySubmitStopCheckpoint(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || !rc.replication.stopping || rc.replication.checkpointInflight {
		return
	}
	if !rc.replication.nextCheckpointAt.IsZero() && now.Before(rc.replication.nextCheckpointAt) {
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreCheckpoint(context.Background(), rc.state.ID, fence, ch.Checkpoint{HW: rc.state.HW}); err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.nextCheckpointAt = now.Add(rc.replication.backoff)
		rc.replication.lastError = err
		return
	}
	rc.replication.checkpointInflight = true
	rc.replication.checkpointOpID = opID
	rc.replication.nextCheckpointAt = time.Time{}
}

// tryEvictStoppedFollower retries runtime deletion after the stopped ACK has already reached the leader.
func (r *Reactor) tryEvictStoppedFollower(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || !rc.replication.stopping || !rc.replication.stopAcked {
		return
	}
	if !rc.replication.nextStopEvictAt.IsZero() && now.Before(rc.replication.nextStopEvictAt) {
		return
	}
	rc.replication.nextStopEvictAt = time.Time{}
	if r.evictRuntimeChannel(rc.state.Key, rc, "stopped ack retry") {
		r.clearAppendSubmitState(rc.state.Key)
		return
	}
	rc.replication.nextStopEvictAt = now.Add(r.cfg.IdleEvictCheckInterval)
}

// handleNotify accepts the legacy transport compatibility nudge and maps it to
// the current PullHint-driven follower resume path.
func (r *Reactor) handleNotify(event Event) {
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
	r.tickReplication(rc, now)
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
		r.tickReplication(rc, now)
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
		if rc.replication.dirty || resp.LeaderLEO > rc.state.LEO || resp.ActivityVersion < rc.replication.lastActivityVersion {
			rc.replication.markDirty(now)
			r.tickReplication(rc, now)
			return
		}
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

func (r *Reactor) handleFollowerStopControl(rc *runtimeChannel, resp transport.PullResponse, now time.Time) {
	if rc == nil || rc.state == nil {
		return
	}
	view := runtimeViewFromChannel(rc, now, AppendFenceView{})
	decision := rc.runtimeLifecycle.OnFollowerLifecycleEvent(FollowerLifecycleEvent{
		Kind:            FollowerLifecycleStopOffered,
		Now:             now,
		LeaderLEO:       resp.LeaderLEO,
		LeaderHW:        resp.LeaderHW,
		ActivityVersion: resp.ActivityVersion,
	}, view, r.lifecycleConfig())
	if !decisionHasAction(decision, LifecycleActionStartFollowerStopCheckpoint) {
		rc.replication.markDirty(now)
		r.tickReplication(rc, now)
		return
	}
	rc.state.HW = minUint64(rc.state.LEO, resp.LeaderHW)
	rc.replication.stopping = true
	rc.replication.stopActivityVersion = resp.ActivityVersion
	rc.replication.deleteAfterStoppedAck = true
	rc.replication.parked = false
	rc.replication.dirty = false
	rc.replication.nextPullAt = time.Time{}
	rc.replication.nextPullAfter = 0
	r.applyLifecycleDecision(rc, decision, now)
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
	rc.replication.pendingPull = nil
	rc.replication.applyBlocked = false
	rc.replication.applyRetryAt = time.Time{}
	rc.replication.backoff = 0
	rc.replication.lastError = nil
	rc.replication.dirty = true
	if !r.submitAck(rc, rc.state.LEO, now) {
		return
	}
}

func (r *Reactor) handleRPCAckResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	current := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: rc.replication.ackOpID}
	ackStopped := rc.replication.ackStopped
	ackActivityVersion := rc.replication.ackActivityVersion
	if !rc.replication.applyAckResult(result, current, time.Now()) {
		return
	}
	now := time.Now()
	if result.Err != nil {
		if ackStopped && errors.Is(result.Err, ch.ErrStaleMeta) {
			rc.replication.cancelStopping()
			rc.replication.backoff = 0
			rc.replication.lastError = result.Err
			rc.replication.markDirty(now)
			r.tickReplication(rc, now)
			r.scheduleReplicationFromState(rc, now)
			return
		}
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.nextAckAt = now.Add(rc.replication.backoff)
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.backoff = 0
	if ackStopped && rc.replication.deleteAfterStoppedAck && ackActivityVersion == rc.replication.stopActivityVersion {
		rc.runtimeLifecycle.FollowerPhase = FollowerLifecycleStopAcking
		rc.replication.stopAcked = true
		rc.replication.nextStopEvictAt = time.Time{}
		if !r.evictRuntimeChannel(rc.state.Key, rc, "stopped ack") {
			rc.replication.nextStopEvictAt = now.Add(r.cfg.IdleEvictCheckInterval)
			r.scheduleReplicationFromState(rc, now)
		} else {
			r.clearAppendSubmitState(rc.state.Key)
		}
		return
	}
	rc.replication.dirty = false
	if rc.replication.pendingPull != nil {
		r.trySubmitPendingApply(rc, now)
		r.scheduleReplicationFromState(rc, now)
		return
	}
	rc.replication.nextPullAt = now
	r.scheduleReplicationFromState(rc, now)
}

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

func (r *Reactor) mergeLeaderPullCacheSuffix(rc *runtimeChannel, waiter *pullWaiter, records []ch.Record) []ch.Record {
	if rc == nil || rc.state == nil || waiter == nil || !waiter.mergeCacheSuffix || waiter.maxBytes <= 0 {
		return records
	}
	used := recordsBytes(records)
	if used >= waiter.maxBytes {
		return records
	}
	next := waiter.nextOffset
	if len(records) > 0 {
		next = records[len(records)-1].Index + 1
	}
	if next == 0 || next > rc.state.LEO {
		return records
	}
	suffix, ok := strictRecentRecordCacheSlice(&rc.recentRecords, next, rc.state.LEO, waiter.maxBytes-used)
	if !ok || len(suffix) == 0 {
		return records
	}
	merged := make([]ch.Record, 0, len(records)+len(suffix))
	merged = append(merged, records...)
	merged = append(merged, suffix...)
	return merged
}

func strictRecentRecordCacheSlice(cache *recentRecordCache, from uint64, maxOffset uint64, maxBytes int) ([]ch.Record, bool) {
	records, ok := cache.slice(from, maxOffset, 0)
	if !ok {
		return nil, false
	}
	if maxBytes <= 0 {
		return []ch.Record{}, true
	}
	out := make([]ch.Record, 0, len(records))
	used := 0
	for _, record := range records {
		size := cacheRecordSize(record)
		if used+size > maxBytes {
			break
		}
		used += size
		out = append(out, record)
	}
	return out, true
}

func (r *Reactor) handleStoreReadLogResult(result worker.Result) {
	rc, err := r.lookup(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	waiter := rc.pullWaiters[result.Fence.OpID]
	if waiter == nil {
		return
	}
	delete(rc.pullWaiters, result.Fence.OpID)
	r.unregisterPullCancelContext(rc)
	future := waiter.future
	if future == nil {
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch || rc.state.Role != ch.RoleLeader {
		future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if result.Err != nil {
		future.Complete(Result{Err: result.Err})
		return
	}
	if result.StoreReadLog == nil {
		future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	records := r.mergeLeaderPullCacheSuffix(rc, waiter, result.StoreReadLog.Records)
	r.completeLeaderPull(rc, waiter, future, records, time.Now())
}

func (r *Reactor) completeLeaderPull(rc *runtimeChannel, waiter *pullWaiter, future *Future, records []ch.Record, now time.Time) {
	if future == nil {
		return
	}
	version := rc.lifecycle.ActivityVersion
	if version == 0 {
		version = rc.state.LEO
	}
	delay := time.Duration(0)
	control := transport.PullControlContinue
	if len(records) == 0 {
		r.syncLeaderFollowers(rc)
		if r.leaderCanOfferStop(rc, now) && waiter.nextOffset == rc.state.LEO+1 {
			if follower := rc.followers[waiter.follower]; follower != nil && follower.Match >= rc.state.LEO {
				control = transport.PullControlStop
				follower.StopOffered = true
				follower.StopOfferedVersion = version
			}
		}
		if control != transport.PullControlStop {
			delay = r.leaderPullDelay(rc, now)
		}
	}
	r.updateLeaderPullFollowerState(rc, waiter, records, control, delay, now)
	future.Complete(Result{Pull: transport.PullResponse{
		ChannelKey:      rc.state.Key,
		Epoch:           rc.state.Epoch,
		LeaderEpoch:     rc.state.LeaderEpoch,
		LeaderHW:        rc.state.HW,
		LeaderLEO:       rc.state.LEO,
		ActivityVersion: version,
		NextPullAfter:   delay,
		Control:         control,
		Records:         records,
	}})
	r.scheduleLifecycleFromState(rc, now)
}

func (r *Reactor) updateLeaderPullFollowerState(rc *runtimeChannel, waiter *pullWaiter, records []ch.Record, control transport.PullControl, delay time.Duration, now time.Time) {
	if waiter == nil {
		return
	}
	r.syncLeaderFollowers(rc)
	if follower := rc.followers[waiter.follower]; follower != nil {
		if len(records) == 0 && control != transport.PullControlStop {
			follower.Parked = delay > 0
			follower.NextExpectedPullAt = now.Add(delay)
		} else if control == transport.PullControlStop {
			follower.Parked = false
			follower.NextExpectedPullAt = time.Time{}
		} else {
			follower.Parked = false
			follower.NextExpectedPullAt = time.Time{}
		}
	}
}

func (r *Reactor) backoffPull(rc *runtimeChannel, err error, now time.Time) {
	rc.replication.pullInflight = false
	rc.replication.pullOpID = 0
	rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
	rc.replication.nextPullAt = now.Add(rc.replication.backoff)
	rc.replication.lastError = err
}

func (r *Reactor) leaderPullDelay(rc *runtimeChannel, now time.Time) time.Duration {
	minDelay := r.cfg.IdlePullMinInterval
	maxDelay := r.cfg.IdlePullMaxInterval
	if minDelay <= 0 {
		minDelay = r.cfg.ReplicationIdlePollInterval
	}
	if minDelay <= 0 {
		minDelay = time.Millisecond
	}
	if maxDelay <= 0 || maxDelay < minDelay {
		maxDelay = minDelay
	}
	idleSince := leaderIdleSince(rc)
	if idleSince.IsZero() {
		return minDelay
	}
	idleAge := now.Sub(idleSince)
	if idleAge < r.cfg.IdleSlowdownAfter {
		return minDelay
	}
	delay := minDelay
	for remaining := idleAge - r.cfg.IdleSlowdownAfter; remaining >= delay && delay < maxDelay; remaining -= delay {
		delay *= 2
		if delay > maxDelay {
			return maxDelay
		}
	}
	return delay
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
		!replication.stopping &&
		!replication.checkpointInflight
}
