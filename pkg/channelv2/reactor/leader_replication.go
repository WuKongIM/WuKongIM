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
	rc, err := r.lookup(event.Key)
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
	if event.Pull.ChannelKey != rc.state.Key || event.Pull.ChannelID != rc.state.ID || event.Pull.Epoch != rc.state.Epoch || event.Pull.LeaderEpoch != rc.state.LeaderEpoch || !rc.state.IsReplica(event.Pull.Follower) {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if event.OpID == 0 || event.Pull.NextOffset == 0 || event.Pull.MaxBytes <= 0 {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
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
	if follower := rc.followers[event.Pull.Follower]; follower != nil {
		follower.LastPullAt = now
		follower.Parked = false
		follower.Stopped = false
		follower.NextExpectedPullAt = time.Time{}
		retireFollowerPullHints(rc, event.Pull.Follower)
		match := event.Pull.NextOffset - 1
		if event.Pull.NextOffset <= rc.state.LEO+1 && match > follower.Match {
			follower.Match = match
		}
	}
	waiter := &pullWaiter{future: event.Future, ctx: ctx, follower: event.Pull.Follower, nextOffset: event.Pull.NextOffset, maxBytes: event.Pull.MaxBytes}
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
	rc, err := r.lookup(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc.state.Role != ch.RoleLeader || event.Ack.ChannelKey != rc.state.Key || event.Ack.Epoch != rc.state.Epoch || event.Ack.LeaderEpoch != rc.state.LeaderEpoch || !rc.state.IsReplica(event.Ack.Follower) {
		if event.Ack.Stopped {
			event.Future.Complete(Result{Err: ch.ErrStaleMeta})
			return
		}
		event.Future.Complete(Result{})
		return
	}
	r.syncLeaderFollowers(rc)
	if event.Ack.Stopped {
		if event.Ack.ActivityVersion != rc.lifecycle.ActivityVersion || event.Ack.MatchOffset != rc.state.LEO {
			event.Future.Complete(Result{Err: ch.ErrStaleMeta})
			return
		}
		if follower := rc.followers[event.Ack.Follower]; follower != nil {
			if follower.StopOffered && follower.StopOfferedVersion != event.Ack.ActivityVersion {
				event.Future.Complete(Result{Err: ch.ErrStaleMeta})
				return
			}
			wasStopped := follower.Stopped && follower.StopAckVersion == event.Ack.ActivityVersion
			follower.Stopped = true
			follower.StopAckVersion = event.Ack.ActivityVersion
			follower.Parked = false
			retireFollowerPullHints(rc, event.Ack.Follower)
			if event.Ack.MatchOffset > follower.Match {
				follower.Match = event.Ack.MatchOffset
			}
			if !wasStopped {
				r.observeFollowerStopped(rc.state.Key, event.Ack.Follower, event.Ack.ActivityVersion)
			}
		}
		decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: event.Ack.Follower, MatchOffset: event.Ack.MatchOffset})
		r.completeReplies(rc, decision.Replies, nil)
		now := time.Now()
		r.tryEvictLeader(rc, now)
		r.scheduleLifecycleFromState(rc, now)
		event.Future.Complete(Result{})
		return
	}
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: event.Ack.Follower, MatchOffset: event.Ack.MatchOffset})
	if follower := rc.followers[event.Ack.Follower]; follower != nil && event.Ack.MatchOffset > follower.Match {
		follower.Match = event.Ack.MatchOffset
	}
	if follower := rc.followers[event.Ack.Follower]; follower != nil && follower.Match >= rc.state.LEO {
		retireFollowerPullHints(rc, event.Ack.Follower)
	}
	r.completeReplies(rc, decision.Replies, nil)
	r.scheduleLifecycleFromState(rc, time.Now())
	event.Future.Complete(Result{})
}

func (r *Reactor) applyLeaderPullAckOffset(rc *runtimeChannel, req transport.PullRequest) error {
	if req.AckOffset == 0 {
		return nil
	}
	if req.AckOffset > rc.state.LEO {
		return ch.ErrStaleMeta
	}
	decision := rc.state.ApplyFollowerAck(machine.FollowerAck{Follower: req.Follower, MatchOffset: req.AckOffset})
	if follower := rc.followers[req.Follower]; follower != nil && req.AckOffset > follower.Match {
		follower.Match = req.AckOffset
	}
	if follower := rc.followers[req.Follower]; follower != nil && follower.Match >= rc.state.LEO {
		retireFollowerPullHints(rc, req.Follower)
	}
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
