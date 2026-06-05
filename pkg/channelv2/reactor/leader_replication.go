package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) handleLeaderPull(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	ctx := event.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleLeader {
		event.Future.Complete(Result{Err: ch.ErrNotLeader})
		return
	}
	if event.Pull.ChannelKey != rc.state.Key || event.Pull.ChannelID != rc.state.ID || event.Pull.Epoch != rc.state.Epoch || event.Pull.LeaderEpoch != rc.state.LeaderEpoch {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if invalidLeaderPullShape(event) {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	if !rc.state.IsReplica(event.Pull.Follower) {
		event.Future.Complete(Result{Err: ch.ErrNotReplica})
		return
	}
	if event.Pull.NeedMeta && rc.state.Status != ch.StatusActive {
		event.Future.Complete(Result{Err: ch.ErrNotReady})
		return
	}
	if rc.pullWaiters == nil {
		rc.pullWaiters = make(map[ch.OpID]*pullWaiter)
	}
	if _, ok := rc.pullWaiters[event.OpID]; ok {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	now := time.Now()
	r.syncLeaderFollowers(rc)
	if err := r.applyLeaderPullAckOffset(rc, event.Pull); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	rc.lifecycle.recordFollowerPull(event.Pull.Follower, event.Pull.NextOffset, rc.state.LEO, now)
	waiter := &pullWaiter{future: event.Future, ctx: ctx, follower: event.Pull.Follower, nextOffset: event.Pull.NextOffset, maxBytes: event.Pull.MaxBytes, needMeta: event.Pull.NeedMeta}
	if r.tryCompletePullFromLeaderCache(rc, event, waiter, now) {
		return
	}
	maxOffset, mergeCacheSuffix := leaderPullReadRange(rc, event.Pull.NextOffset)
	waiter.mergeCacheSuffix = mergeCacheSuffix
	rc.pullWaiters[event.OpID] = waiter
	r.registerPullCancelContext(rc, ctx)
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: event.OpID}
	err = r.submitStoreReadLog(ctx, event.Pull.ChannelID, fence, event.Pull.NextOffset, maxOffset, event.Pull.MaxBytes)
	if err != nil {
		delete(rc.pullWaiters, event.OpID)
		r.unregisterPullCancelContext(rc)
		event.Future.Complete(Result{Err: err})
	}
}

// invalidLeaderPullShape rejects request fields that are malformed without consulting runtime metadata.
func invalidLeaderPullShape(event Event) bool {
	return event.OpID == 0 || event.Pull.NextOffset == 0 || event.Pull.MaxBytes <= 0
}

func (r *Reactor) tryCompletePullFromLeaderCache(rc *runtimeChannel, event Event, waiter *pullWaiter, now time.Time) bool {
	if rc == nil || rc.state == nil || !rc.recentRecords.enabled() {
		return false
	}
	nextOffset := event.Pull.NextOffset
	if nextOffset == rc.state.LEO+1 {
		r.completeLeaderPull(rc, waiter, event.Future, nil, now)
		return true
	}
	if nextOffset > rc.state.LEO+1 {
		return false
	}
	records, ok := rc.recentRecords.slice(nextOffset, rc.state.LEO, waiter.maxBytes)
	if !ok {
		return false
	}
	r.completeLeaderPull(rc, waiter, event.Future, records, now)
	return true
}

func leaderPullReadRange(rc *runtimeChannel, nextOffset uint64) (maxOffset uint64, mergeCacheSuffix bool) {
	if rc == nil || rc.state == nil {
		return 0, false
	}
	if rc.recentRecords.enabled() && rc.recentRecords.hasSuffixAfter(nextOffset) {
		base := rc.recentRecords.base()
		if base > 0 {
			return base - 1, true
		}
	}
	return rc.state.LEO, false
}

func (r *Reactor) handleLeaderAck(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleLeader || event.Ack.ChannelKey != rc.state.Key || event.Ack.Epoch != rc.state.Epoch || event.Ack.LeaderEpoch != rc.state.LeaderEpoch || !rc.state.IsReplica(event.Ack.Follower) {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	r.syncLeaderFollowers(rc)
	if !event.Ack.Stopped {
		if err := r.applyLeaderProgressAck(rc, event.Ack); err != nil {
			event.Future.Complete(Result{Err: err})
			return
		}
		event.Future.Complete(Result{})
		return
	}
	if event.Ack.ActivityVersion != rc.lifecycle.version || event.Ack.MatchOffset != rc.state.LEO {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	accepted := rc.lifecycle.markFollowerStopped(event.Ack.Follower, event.Ack.ActivityVersion, event.Ack.MatchOffset)
	if follower := rc.lifecycle.followers[event.Ack.Follower]; follower == nil || !follower.stopped(event.Ack.ActivityVersion) {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if accepted {
		r.observeFollowerStopped(rc.state.Key, event.Ack.Follower, event.Ack.ActivityVersion)
	}
	oldHW := rc.state.HW
	now := time.Now()
	r.markAppendAckOffsetObserved(rc, event.Ack.MatchOffset, now)
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: event.Ack.Follower, MatchOffset: event.Ack.MatchOffset})
	r.markAppendHWAdvanced(rc, oldHW, rc.state.HW, now)
	r.completeReplies(rc, decision.Replies, nil)
	r.driveLifecycle(rc, lifecycleEvent{kind: lifecycleEventLeaderStoppedAck, now: now})
	r.scheduleLifecycleFromState(rc, now)
	event.Future.Complete(Result{})
}

func (r *Reactor) applyLeaderProgressAck(rc *runtimeChannel, req transport.AckRequest) error {
	if req.MatchOffset == 0 {
		return nil
	}
	if req.MatchOffset > rc.state.LEO {
		return ch.ErrStaleMeta
	}
	oldHW := rc.state.HW
	now := time.Now()
	r.markAppendAckOffsetObserved(rc, req.MatchOffset, now)
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: req.Follower, MatchOffset: req.MatchOffset})
	r.markAppendHWAdvanced(rc, oldHW, rc.state.HW, now)
	rc.lifecycle.recordFollowerProgress(req.Follower, req.MatchOffset, rc.state.LEO)
	r.completeReplies(rc, decision.Replies, nil)
	return nil
}

func (r *Reactor) applyLeaderPullAckOffset(rc *runtimeChannel, req transport.PullRequest) error {
	if req.AckOffset == 0 {
		return nil
	}
	if req.AckOffset > rc.state.LEO {
		return ch.ErrStaleMeta
	}
	oldHW := rc.state.HW
	now := time.Now()
	r.markAppendAckOffsetObserved(rc, req.AckOffset, now)
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: req.Follower, MatchOffset: req.AckOffset})
	r.markAppendHWAdvanced(rc, oldHW, rc.state.HW, now)
	rc.lifecycle.recordFollowerProgress(req.Follower, req.AckOffset, rc.state.LEO)
	r.completeReplies(rc, decision.Replies, nil)
	return nil
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
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
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
	version := rc.lifecycle.version
	if version == 0 {
		version = rc.state.LEO
	}
	delay := time.Duration(0)
	control := transport.PullControlContinue
	if len(records) == 0 {
		r.syncLeaderFollowers(rc)
		if r.leaderCanOfferStop(rc, now) && waiter.nextOffset == rc.state.LEO+1 {
			if follower := rc.lifecycle.followers[waiter.follower]; follower != nil && follower.match >= rc.state.LEO {
				control = transport.PullControlStop
				rc.lifecycle.offerStop(waiter.follower)
			}
		}
		if control != transport.PullControlStop {
			delay = r.leaderPullDelay(rc, now)
		}
	}
	r.markAppendFollowerPullServed(rc, records, now)
	r.updateLeaderPullFollowerState(rc, waiter, records, control, delay, now)
	resp := transport.PullResponse{
		ChannelKey:      rc.state.Key,
		Epoch:           rc.state.Epoch,
		LeaderEpoch:     rc.state.LeaderEpoch,
		LeaderHW:        rc.state.HW,
		LeaderLEO:       rc.state.LEO,
		ActivityVersion: version,
		NextPullAfter:   delay,
		Control:         control,
		Records:         records,
	}
	if waiter != nil && waiter.needMeta {
		meta := cloneLeaderRuntimeMeta(rc)
		resp.Meta = &meta
	}
	future.Complete(Result{Pull: resp})
	r.scheduleLifecycleFromState(rc, now)
}

// cloneLeaderRuntimeMeta snapshots runtime metadata with independent membership slices for RPC callers.
func cloneLeaderRuntimeMeta(rc *runtimeChannel) ch.Meta {
	if rc == nil || rc.state == nil {
		return ch.Meta{}
	}
	return ch.Meta{
		Key:         rc.state.Key,
		ID:          rc.state.ID,
		Epoch:       rc.state.Epoch,
		LeaderEpoch: rc.state.LeaderEpoch,
		Leader:      rc.state.Leader,
		Replicas:    append([]ch.NodeID(nil), rc.state.Replicas...),
		ISR:         append([]ch.NodeID(nil), rc.state.ISR...),
		MinISR:      rc.state.MinISR,
		Status:      rc.state.Status,
	}
}

func (r *Reactor) updateLeaderPullFollowerState(rc *runtimeChannel, waiter *pullWaiter, records []ch.Record, control transport.PullControl, delay time.Duration, now time.Time) {
	if waiter == nil {
		return
	}
	r.syncLeaderFollowers(rc)
	if follower := rc.lifecycle.followers[waiter.follower]; follower != nil {
		if len(records) == 0 && control != transport.PullControlStop {
			follower.parked = delay > 0
			follower.nextExpectedPullAt = now.Add(delay)
		} else if control == transport.PullControlStop {
			follower.parked = false
			follower.nextExpectedPullAt = time.Time{}
		} else {
			follower.parked = false
			follower.nextExpectedPullAt = time.Time{}
			if len(records) > 0 && follower.match < rc.state.LEO && !follower.hint.inflight {
				follower.hint.retryAt = retryDue(now, r.cfg.PullHintRetryInterval)
			}
		}
	}
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
