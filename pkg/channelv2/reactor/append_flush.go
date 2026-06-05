package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
)

func (r *Reactor) nextBatchOpID() ch.OpID {
	if r.cfg.NextOpID != nil {
		return r.cfg.NextOpID()
	}
	return ch.OpID(1<<63 + r.nextOp.Add(1))
}

func (r *Reactor) tryFlushAppend(rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil {
		return
	}
	defer r.scheduleAppendFlushFromState(rc)
	r.sweepAppendCancellationsForChannelAt(rc, now)
	if rc.appendInflight != nil {
		return
	}
	if rc.appendStoreBlocked && now.Before(rc.appendRetryAt) {
		return
	}
	if !rc.appendQ.shouldFlush(now) {
		return
	}
	batch := rc.appendQ.popBatch(r.nextBatchOpID(), rc.state)
	r.observeAppendQueuePressure(rc)
	if len(batch.requests) == 0 {
		rc.appendQ.storeBlocked = false
		return
	}
	decision := rc.state.ProposeAppendBatch(machine.AppendBatchCommand{
		BatchOpID: batch.batchOpID,
		Waiters:   appendBatchWaiters(batch.requests),
	})
	if decision.Err != nil {
		rc.appendQ.storeBlocked = false
		r.failAppendBatch(rc, batch, decision.Err)
		return
	}
	if len(decision.Tasks) == 0 {
		rc.appendQ.storeBlocked = false
		return
	}
	task := decision.Tasks[0]
	batch.fence = task.Fence
	batch.records = task.StoreAppend.Records
	batch.trace = selectAppendTraceBatch(batch)
	if err := r.submitStoreAppend(context.Background(), batch.requests[0].req.ChannelID, task); err != nil {
		rc.state.AbortAppendBatchProposal(batch.batchOpID)
		if errors.Is(err, ch.ErrBackpressured) {
			rc.appendQ.restoreFront(batch)
			rc.appendStoreBlocked = true
			rc.appendRetryAt = now.Add(r.cfg.AppendStoreRetryBackoff)
			r.observeAppendQueuePressure(rc)
			return
		}
		rc.appendQ.storeBlocked = false
		r.failAppendBatch(rc, batch, err)
		return
	}
	r.markAppendStoreSubmitted(rc, batch, now)
	rc.appendInflight = &batch
	rc.appendStoreBlocked = false
	rc.appendRetryAt = time.Time{}
	r.observeAppendBatch(batch, now)
}

func appendBatchWaiters(requests []appendRequest) []machine.AppendBatchWaiter {
	waiters := make([]machine.AppendBatchWaiter, 0, len(requests))
	for _, req := range requests {
		waiters = append(waiters, machine.AppendBatchWaiter{
			OpID:              req.opID,
			CommitMode:        req.commitMode,
			OmitResultPayload: req.req.OmitResultPayload,
			Records:           req.records,
		})
	}
	return waiters
}

func (r *Reactor) sweepAppendCancellations() {
	r.sweepAppendCancellationsAt(time.Now())
}

func (r *Reactor) sweepAppendCancellationsAt(now time.Time) {
	if r == nil || len(r.appendCancelChannels) == 0 {
		return
	}
	if !r.appendCancelSweepNextAt.IsZero() && now.Before(r.appendCancelSweepNextAt) {
		return
	}
	r.appendCancelSweepNextAt = now.Add(defaultAppendCancelSweepInterval)
	for key, rc := range r.appendCancelChannels {
		if rc == nil || len(rc.appendCancelContexts) == 0 {
			delete(r.appendCancelChannels, key)
			continue
		}
		r.sweepAppendCancellationsForChannelAt(rc, now)
		if len(rc.appendCancelContexts) == 0 {
			delete(r.appendCancelChannels, key)
		}
	}
}

func (r *Reactor) sweepAppendCancellationsForChannel(rc *runtimeChannel) {
	r.sweepAppendCancellationsForChannelAt(rc, time.Now())
}

func (r *Reactor) sweepAppendCancellationsForChannelAt(rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil || len(rc.appendCancelContexts) == 0 {
		return
	}
	r.nudgePendingQuorumFollowersFromCancellationSweep(rc, now)
	for opID, ctx := range rc.appendCancelContexts {
		if ctx == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			r.cancelAppendWaiter(rc, opID, err)
		}
	}
}

func (r *Reactor) nudgePendingQuorumFollowersFromCancellationSweep(rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil {
		return
	}
	if !rc.appendCancelNudgeNextAt.IsZero() && now.Before(rc.appendCancelNudgeNextAt) {
		return
	}
	interval := r.cfg.PullHintRetryInterval
	if interval <= 0 {
		interval = time.Second
	}
	rc.appendCancelNudgeNextAt = now.Add(interval)
	r.nudgePendingQuorumFollowers(rc, now)
}

func (r *Reactor) cancelAppendWaiter(rc *runtimeChannel, opID ch.OpID, cancelErr error) bool {
	if r == nil || rc == nil {
		return false
	}
	if cancelErr == nil {
		cancelErr = context.Canceled
	}
	r.observeAppendWaitCanceled(rc, opID, cancelErr)
	r.unregisterAppendCancelContext(rc, opID)
	if req, ok := rc.appendQ.remove(opID); ok {
		future := req.future
		if future == nil {
			future = rc.waiters[opID]
		}
		delete(rc.waiters, opID)
		if rc.state != nil {
			rc.state.CancelAppendWaiter(opID)
		}
		if len(rc.appendQ.pending) == 0 {
			rc.appendStoreBlocked = false
			rc.appendRetryAt = time.Time{}
		}
		r.observeAppendQueuePressure(rc)
		r.completeAppendFuture(rc, opID, future, Result{Err: cancelErr})
		return true
	}
	future := rc.waiters[opID]
	if future != nil {
		delete(rc.waiters, opID)
	}
	if rc.state != nil {
		rc.state.CancelAppendWaiter(opID)
	}
	if future != nil {
		r.completeAppendFuture(rc, opID, future, Result{Err: cancelErr})
		return true
	}
	return false
}

func (r *Reactor) failAppendBatch(rc *runtimeChannel, batch appendBatch, err error) {
	for _, req := range batch.requests {
		delete(rc.waiters, req.opID)
		r.unregisterAppendCancelContext(rc, req.opID)
		r.completeAppendFuture(rc, req.opID, req.future, Result{Err: err})
	}
}
