package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *Reactor) loop() {
	idleTimer := time.NewTimer(time.Hour)
	stopTimer(idleTimer)
	defer func() {
		stopTimer(idleTimer)
		r.submitGate.Lock()
		r.failPendingWaiters(ch.ErrClosed)
		r.failQueuedEvents(ch.ErrClosed)
		r.submitGate.Unlock()
		close(r.done)
	}()
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		events := r.mailbox.DrainInto(r.drainBuf, defaultReactorDrain)
		r.observeAllMailboxDepths()
		if len(events) == 0 {
			r.sweepAppendCancellations()
			r.sweepPullCancellations()
			r.sweepLookupCancellations()
			now := time.Now()
			r.processDue(now)
			resetTimer(idleTimer, r.idleWait(now))
			event, ok := r.mailbox.WaitOne(r.stop, idleTimer.C)
			stopTimer(idleTimer)
			if !ok {
				select {
				case <-r.stop:
					return
				default:
				}
				continue
			}
			r.handle(event)
			continue
		}
		for i := range events {
			r.handle(events[i])
			events[i] = Event{}
		}
		r.drainBuf = events[:0]
		r.processDue(time.Now())
	}
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if d < 0 {
		d = 0
	}
	stopTimer(timer)
	timer.Reset(d)
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (r *Reactor) idleWait(now time.Time) time.Duration {
	wait := r.due.nextWait(now)
	if wait > time.Millisecond && r.hasCancelableWaiters() {
		return time.Millisecond
	}
	return wait
}

func (r *Reactor) handle(event Event) {
	var started time.Time
	if r != nil && r.cfg.SlowEventThreshold > 0 {
		started = time.Now()
		defer func() { r.observeSlowEvent(event.Kind, time.Since(started)) }()
	}
	r.sweepCancelableWaiters()
	switch event.Kind {
	case EventApplyMeta:
		r.handleApplyMeta(event)
	case EventCheckState:
		r.handleCheckState(event)
	case EventLookupCommittedMessage:
		r.handleLookupCommittedMessage(event)
	case EventRuntimeSnapshot:
		r.handleRuntimeSnapshot(event)
	case EventRuntimeProbe:
		r.handleRuntimeProbe(event)
	case EventDrainChannel:
		r.handleDrainChannel(event)
	case EventRuntimeEvict:
		r.handleRuntimeEvict(event)
	case EventRetentionView:
		r.handleRetentionView(event)
	case EventApplyRetentionBoundary:
		r.handleApplyRetentionBoundary(event)
	case EventAppend:
		r.handleAppend(event)
	case EventCancelWaiter:
		r.handleCancelWaiter(event)
	case EventPull:
		r.handleLeaderPull(event)
	case EventAck:
		r.handleLeaderAck(event)
	case EventNotify:
		r.handleLegacyFollowerNotify(event)
	case EventPullHint:
		r.handleFollowerPullHint(event)
	case EventLeaderEvictReady:
		r.handleLeaderEvictReady(event)
	case EventWorkerResult:
		r.handleWorkerResult(event)
	case EventTick:
		r.handleTick(event)
	case EventClose:
		r.handleClose(event)
	}
	r.sweepCancelableWaiters()
}

func (r *Reactor) handleTick(event Event) {
	now := event.TickNow
	if now.IsZero() {
		now = time.Now()
	}
	r.processDue(now)
	if event.Key != "" {
		if rc := r.channels[event.Key]; rc != nil {
			r.tryFlushAppend(rc, now)
			r.tickFollowerReplication(rc, now)
			r.tickLifecycleController(rc, now)
		}
	}
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}

func (r *Reactor) handleClose(event Event) {
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
	r.once.Do(func() { close(r.stop) })
}

func (r *Reactor) failPendingWaiters(err error) {
	if r == nil {
		return
	}
	r.clearAllLoadedMetaRefreshes()
	for key, rc := range r.channels {
		if rc != nil && rc.loading != nil && rc.state == nil && rc.pending == nil {
			r.completeStoreLoadFutures(rc.loading, Result{Err: err})
			delete(r.channels, key)
			continue
		}
		if rc != nil && rc.pending != nil && rc.state == nil {
			r.releasePendingMeta(key, rc, err)
			continue
		}
		r.failWaiters(rc, err)
		r.clearAppendCancelContexts(rc)
		r.clearPullCancelChannel(rc)
		r.clearLookupCancelChannel(rc)
	}
}

func (r *Reactor) failQueuedEvents(err error) {
	if r == nil || r.mailbox == nil {
		return
	}
	for {
		events := r.mailbox.Drain(defaultReactorDrain)
		if len(events) == 0 {
			return
		}
		for _, event := range events {
			if event.Future != nil {
				event.Future.Complete(Result{Err: err})
			}
		}
	}
}

func (r *Reactor) handleCancelWaiter(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		if event.Future != nil {
			event.Future.Complete(Result{})
		}
		return
	}
	cancelErr := event.CancelErr
	if cancelErr == nil {
		cancelErr = context.Canceled
	}
	r.cancelAppendWaiter(rc, event.CancelOp, cancelErr)
	r.cancelPullWaiter(rc, event.CancelOp, cancelErr)
	r.cancelLookupWaiter(rc, event.CancelOp, cancelErr)
	if event.Future != nil {
		event.Future.Complete(Result{})
	}
}
