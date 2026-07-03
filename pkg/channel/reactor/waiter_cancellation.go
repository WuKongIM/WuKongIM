package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (rc *runtimeChannel) addWaiter(opID ch.OpID, future *Future) error {
	if rc == nil || future == nil {
		return nil
	}
	if rc.waiters == nil {
		rc.waiters = make(map[ch.OpID]*Future)
	}
	if _, ok := rc.waiters[opID]; ok {
		return ch.ErrInvalidConfig
	}
	rc.waiters[opID] = future
	return nil
}

func (r *Reactor) registerAppendCancelContext(rc *runtimeChannel, opID ch.OpID, ctx context.Context) {
	if r == nil || rc == nil || ctx == nil {
		return
	}
	if ctx.Done() == nil {
		return
	}
	if rc.appendCancelContexts == nil {
		rc.appendCancelContexts = make(map[ch.OpID]context.Context)
	}
	rc.appendCancelContexts[opID] = ctx
	if r.appendCancelChannels == nil {
		r.appendCancelChannels = make(map[ch.ChannelKey]*runtimeChannel)
	}
	if rc.state != nil {
		r.appendCancelChannels[rc.state.Key] = rc
	}
}

func (r *Reactor) unregisterAppendCancelContext(rc *runtimeChannel, opID ch.OpID) {
	if r == nil || rc == nil || len(rc.appendCancelContexts) == 0 {
		return
	}
	delete(rc.appendCancelContexts, opID)
	if len(rc.appendCancelContexts) == 0 && r.appendCancelChannels != nil && rc.state != nil {
		delete(r.appendCancelChannels, rc.state.Key)
	}
}

func (r *Reactor) clearAppendCancelContexts(rc *runtimeChannel) {
	if r == nil || rc == nil {
		return
	}
	rc.appendCancelContexts = nil
	rc.appendCancelNudgeNextAt = time.Time{}
	if r.appendCancelChannels != nil && rc.state != nil {
		delete(r.appendCancelChannels, rc.state.Key)
	}
}

func (r *Reactor) registerPullCancelContext(rc *runtimeChannel, ctx context.Context) {
	r.registerCancelChannel(&r.pullCancelChannels, rc, ctx)
}

func (r *Reactor) unregisterPullCancelContext(rc *runtimeChannel) {
	r.unregisterCancelChannel(r.pullCancelChannels, rc, rc.hasCancelablePullWaiters())
}

func (r *Reactor) clearPullCancelChannel(rc *runtimeChannel) {
	clearCancelChannel(r.pullCancelChannels, rc)
}

func (r *Reactor) registerLookupCancelContext(rc *runtimeChannel, ctx context.Context) {
	r.registerCancelChannel(&r.lookupCancelChannels, rc, ctx)
}

func (r *Reactor) unregisterLookupCancelContext(rc *runtimeChannel) {
	r.unregisterCancelChannel(r.lookupCancelChannels, rc, rc.hasCancelableLookupWaiters())
}

func (r *Reactor) clearLookupCancelChannel(rc *runtimeChannel) {
	clearCancelChannel(r.lookupCancelChannels, rc)
}

func (r *Reactor) hasCancelableWaiters() bool {
	return len(r.appendCancelChannels) > 0 || len(r.pullCancelChannels) > 0 || len(r.lookupCancelChannels) > 0
}

func (r *Reactor) sweepCancelableWaiters() {
	r.sweepAppendCancellations()
	r.sweepPullCancellations()
	r.sweepLookupCancellations()
}

func (r *Reactor) registerCancelChannel(index *map[ch.ChannelKey]*runtimeChannel, rc *runtimeChannel, ctx context.Context) {
	if r == nil || index == nil || rc == nil || rc.state == nil || ctx == nil || ctx.Done() == nil {
		return
	}
	if *index == nil {
		*index = make(map[ch.ChannelKey]*runtimeChannel)
	}
	(*index)[rc.state.Key] = rc
}

func (r *Reactor) unregisterCancelChannel(index map[ch.ChannelKey]*runtimeChannel, rc *runtimeChannel, hasCancelable bool) {
	if r == nil || rc == nil || rc.state == nil || index == nil || hasCancelable {
		return
	}
	delete(index, rc.state.Key)
}

func clearCancelChannel(index map[ch.ChannelKey]*runtimeChannel, rc *runtimeChannel) {
	if rc == nil || rc.state == nil || index == nil {
		return
	}
	delete(index, rc.state.Key)
}

// failPendingAppendWaiters completes append waiters that are fenced by accepted metadata.
func (r *Reactor) failPendingAppendWaiters(rc *runtimeChannel, err error) {
	if r == nil || rc == nil {
		return
	}
	for opID, future := range rc.waiters {
		delete(rc.waiters, opID)
		r.unregisterAppendCancelContext(rc, opID)
		r.completeAppendFuture(rc, opID, future, Result{Err: err})
	}
	rc.appendQ.clear()
	r.observeAppendQueuePressure(rc)
	rc.appendInflight = nil
	rc.appendStoreBlocked = false
	rc.appendRetryAt = time.Time{}
	if rc.state != nil && rc.state.InflightAppend != nil {
		rc.state.AbortAppendBatchProposal(rc.state.InflightAppend.OpID)
	}
}

func (r *Reactor) failWaiters(rc *runtimeChannel, err error) {
	if r == nil || rc == nil {
		return
	}
	for opID, future := range rc.waiters {
		delete(rc.waiters, opID)
		if future != nil {
			r.unregisterAppendCancelContext(rc, opID)
			r.completeAppendFuture(rc, opID, future, Result{Err: err})
		}
	}
	rc.appendQ.clear()
	r.observeAppendQueuePressure(rc)
	rc.appendInflight = nil
	rc.failPendingPullWaiters(err)
	rc.failPendingLookupWaiters(err)
	rc.failPendingRetentionWaiters(err)
}

func (r *Reactor) evictRuntimeChannel(key ch.ChannelKey, rc *runtimeChannel, reason string) bool {
	_ = reason
	if r == nil || rc == nil || rc.state == nil || r.channels[key] != rc {
		return false
	}
	if !rc.safeToEvictRuntime() {
		return false
	}
	role := rc.state.Role
	wasParkedFollower := role == ch.RoleFollower && rc.replication.parked
	r.clearAppendCancelContexts(rc)
	r.clearPullCancelChannel(rc)
	r.clearLookupCancelChannel(rc)
	rc.failPendingRetentionWaiters(ch.ErrClosed)
	storeHandle := rc.store
	rc.store = nil
	r.clearAppendQueuePressure(rc)
	delete(r.channels, key)
	r.closeStoreAsync(key, rc.state.Generation, storeHandle)
	r.observeChannelRuntimeEvicted(key, role)
	if wasParkedFollower {
		r.observeFollowerParkedCount(r.countParkedFollowers())
	}
	r.observeRuntimeCounts()
	return true
}

func (rc *runtimeChannel) safeToEvictRuntime() bool {
	return runtimeViewFromChannel(rc, time.Now(), AppendFenceView{}).SafeToEvict()
}

func (rc *runtimeChannel) failPendingPullWaiters(err error) {
	if rc == nil || len(rc.pullWaiters) == 0 {
		return
	}
	for opID, waiter := range rc.pullWaiters {
		delete(rc.pullWaiters, opID)
		if waiter != nil && waiter.future != nil {
			waiter.future.Complete(Result{Err: err})
		}
	}
}

func (rc *runtimeChannel) failPendingLookupWaiters(err error) {
	if rc == nil || len(rc.lookupWaiters) == 0 {
		return
	}
	for opID, waiter := range rc.lookupWaiters {
		delete(rc.lookupWaiters, opID)
		if waiter != nil && waiter.future != nil {
			waiter.future.Complete(Result{Err: err})
		}
	}
}

func (rc *runtimeChannel) hasCancelablePullWaiters() bool {
	if rc == nil {
		return false
	}
	for _, waiter := range rc.pullWaiters {
		if waiter != nil && waiter.ctx != nil && waiter.ctx.Done() != nil {
			return true
		}
	}
	return false
}

func (rc *runtimeChannel) hasCancelableLookupWaiters() bool {
	if rc == nil {
		return false
	}
	for _, waiter := range rc.lookupWaiters {
		if waiter != nil && waiter.ctx != nil && waiter.ctx.Done() != nil {
			return true
		}
	}
	return false
}

func (r *Reactor) sweepCancelChannels(index map[ch.ChannelKey]*runtimeChannel, hasCancelable func(*runtimeChannel) bool, sweep func(*runtimeChannel)) {
	if r == nil || len(index) == 0 {
		return
	}
	for key, rc := range index {
		if rc == nil || !hasCancelable(rc) {
			delete(index, key)
			continue
		}
		sweep(rc)
		if !hasCancelable(rc) {
			delete(index, key)
		}
	}
}

func (r *Reactor) sweepPullCancellations() {
	r.sweepCancelChannels(r.pullCancelChannels, (*runtimeChannel).hasCancelablePullWaiters, func(rc *runtimeChannel) {
		for opID, waiter := range rc.pullWaiters {
			if waiter == nil || waiter.ctx == nil {
				continue
			}
			if err := waiter.ctx.Err(); err != nil {
				r.cancelPullWaiter(rc, opID, err)
			}
		}
	})
}

func (r *Reactor) cancelPullWaiter(rc *runtimeChannel, opID ch.OpID, cancelErr error) bool {
	if r == nil || rc == nil {
		return false
	}
	if cancelErr == nil {
		cancelErr = context.Canceled
	}
	waiter := rc.pullWaiters[opID]
	if waiter == nil {
		r.unregisterPullCancelContext(rc)
		return false
	}
	delete(rc.pullWaiters, opID)
	r.unregisterPullCancelContext(rc)
	if waiter.future != nil {
		waiter.future.Complete(Result{Err: cancelErr})
	}
	return true
}

func (r *Reactor) sweepLookupCancellations() {
	r.sweepCancelChannels(r.lookupCancelChannels, (*runtimeChannel).hasCancelableLookupWaiters, func(rc *runtimeChannel) {
		for opID, waiter := range rc.lookupWaiters {
			if waiter == nil || waiter.ctx == nil {
				continue
			}
			if err := waiter.ctx.Err(); err != nil {
				r.cancelLookupWaiter(rc, opID, err)
			}
		}
	})
}

func (r *Reactor) cancelLookupWaiter(rc *runtimeChannel, opID ch.OpID, cancelErr error) bool {
	if r == nil || rc == nil {
		return false
	}
	if cancelErr == nil {
		cancelErr = context.Canceled
	}
	waiter := rc.lookupWaiters[opID]
	if waiter == nil {
		r.unregisterLookupCancelContext(rc)
		return false
	}
	delete(rc.lookupWaiters, opID)
	r.unregisterLookupCancelContext(rc)
	if waiter.future != nil {
		waiter.future.Complete(Result{Err: cancelErr})
	}
	return true
}
