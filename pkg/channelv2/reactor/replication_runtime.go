package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) tickReplication(rc *runtimeChannel, now time.Time) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleFollower || rc.state.Status != ch.StatusActive {
		return
	}
	if rc.replication.pendingAck {
		r.trySubmitPendingAck(rc, now)
		if rc.replication.pendingAck || rc.replication.ackInflight {
			return
		}
	}
	if rc.replication.ackInflight {
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

func (r *Reactor) tickAllReplication(now time.Time) {
	if r == nil {
		return
	}
	for _, rc := range r.channels {
		r.tickReplication(rc, now)
	}
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
	req := transport.AckRequest{ChannelKey: rc.state.Key, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, Follower: r.cfg.LocalNode, MatchOffset: match}
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
	rc.replication.ackInflight = true
	rc.replication.ackOpID = opID
	rc.replication.ackMatch = match
	return true
}

func (r *Reactor) notifyFollowers(rc *runtimeChannel) {
	if r == nil || rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader || r.cfg.Pools == nil {
		return
	}
	for _, replica := range rc.state.Replicas {
		if replica == r.cfg.LocalNode {
			continue
		}
		fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: r.nextOpID()}
		req := transport.NotifyRequest{
			ChannelKey:  rc.state.Key,
			ChannelID:   rc.state.ID,
			Epoch:       rc.state.Epoch,
			LeaderEpoch: rc.state.LeaderEpoch,
			Leader:      r.cfg.LocalNode,
			LeaderLEO:   rc.state.LEO,
		}
		_ = r.submitRPCNotify(context.Background(), replica, fence, req)
	}
}

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
	if resp.ActivityVersion > rc.replication.lastActivityVersion {
		rc.replication.lastActivityVersion = resp.ActivityVersion
	}
	if len(resp.Records) == 0 {
		rc.state.HW = minUint64(rc.state.LEO, resp.LeaderHW)
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
	if !rc.replication.applyAckResult(result, current, time.Now()) {
		return
	}
	now := time.Now()
	if result.Err != nil {
		rc.replication.backoff = nextReplicationBackoff(rc.replication.backoff, r.cfg.ReplicationMinBackoff, r.cfg.ReplicationMaxBackoff)
		rc.replication.nextAckAt = now.Add(rc.replication.backoff)
		return
	}
	rc.replication.backoff = 0
	rc.replication.dirty = false
	if rc.replication.pendingPull != nil {
		r.trySubmitPendingApply(rc, now)
		return
	}
	rc.replication.nextPullAt = now
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
	records := result.StoreReadLog.Records
	now := time.Now()
	version := rc.lifecycle.ActivityVersion
	if version == 0 {
		version = rc.state.LEO
	}
	delay := time.Duration(0)
	if len(records) == 0 {
		delay = r.leaderPullDelay(rc, now)
	}
	r.syncLeaderFollowers(rc)
	if follower := rc.followers[waiter.follower]; follower != nil {
		if len(records) == 0 {
			follower.Parked = delay > 0
			follower.NextExpectedPullAt = now.Add(delay)
		} else {
			follower.Parked = false
			follower.NextExpectedPullAt = time.Time{}
		}
	}
	future.Complete(Result{Pull: transport.PullResponse{
		ChannelKey:      rc.state.Key,
		Epoch:           rc.state.Epoch,
		LeaderEpoch:     rc.state.LeaderEpoch,
		LeaderHW:        rc.state.HW,
		LeaderLEO:       rc.state.LEO,
		ActivityVersion: version,
		NextPullAfter:   delay,
		Control:         transport.PullControlContinue,
		Records:         records,
	}})
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
	if rc == nil || rc.lifecycle.LastAppendAt.IsZero() {
		return minDelay
	}
	idleAge := now.Sub(rc.lifecycle.LastAppendAt)
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
