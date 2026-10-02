package reactor

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

type willReceiptWaiter struct {
	request ch.WillReceiptRequest
	// committedThrough and writeFence pin the admitted runtime observation.
	committedThrough uint64
	writeFence       ch.WriteFence
}

// validateWillReceiptAdmission requires recovered authority independently of
// CommitReady so a stable write fence cannot be mistaken for failed recovery.
func (r *Reactor) validateWillReceiptAdmission(ctx context.Context, rc *runtimeChannel, q ch.WillReceiptRequest) error {
	if !q.Valid() || r.cfg.QuorumLog == nil {
		return ch.ErrInvalidConfig
	}
	if rc == nil || rc.state == nil || rc.state.ID != q.ChannelID || rc.quorumAuthority.ID.FenceVersion != q.ExpectedRouteGeneration {
		return ch.ErrStaleMeta
	}
	if _, ok := rc.store.(store.WillReceiptLookup); !ok {
		return ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if rc.state.Status == ch.StatusDeleting || rc.state.Status == ch.StatusDeleted {
		return ch.ErrChannelNotFound
	}
	if rc.state.Status != ch.StatusActive || !rc.quorumReadReady || rc.quorumInstall != nil {
		return ch.ErrNotReady
	}
	if rc.state.Role != ch.RoleLeader || rc.state.Leader != r.cfg.LocalNode {
		return ch.ErrNotLeader
	}
	if rc.state.Epoch != q.ExpectedChannelEpoch || rc.state.LeaderEpoch != q.ExpectedLeaderEpoch {
		return ch.ErrStaleMeta
	}
	if r.appendAdmissionGuard != nil {
		if err := r.appendAdmissionGuard.AllowChannelAppend(ctx, ch.AppendAdmissionRequest{ChannelID: rc.state.ID, ChannelKey: rc.state.Key, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, Leader: rc.state.Leader}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// handleWillReceipt fixes HW and lifecycle ownership before leaving the reactor.
func (r *Reactor) handleWillReceipt(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	ctx := event.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err = r.validateWillReceiptAdmission(ctx, rc, event.WillReceipt); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	opID := event.OpID
	if opID == 0 {
		opID = r.nextOpID()
	}
	if err = r.registerLookupWaiter(rc, opID, ctx, 0, event.Future); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	rc.lookupWaiters[opID].will = &willReceiptWaiter{request: event.WillReceipt, committedThrough: rc.state.HW, writeFence: rc.state.WriteFence}
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if r.cfg.Pools == nil {
		err = ch.ErrInvalidConfig
	} else {
		err = r.cfg.Pools.Submit(ctx, worker.Task{Kind: worker.TaskStoreWillReceipt, Context: ctx, Fence: fence, StoreWillReceipt: &worker.StoreWillReceiptTask{Request: event.WillReceipt, CommittedThrough: rc.state.HW}})
	}
	if err != nil {
		delete(rc.lookupWaiters, opID)
		r.unregisterLookupCancelContext(rc)
		event.Future.Complete(Result{Err: err})
	}
}

// handleStoreWillReceiptResult discharges only this query's fenced waiter.
func (r *Reactor) handleStoreWillReceiptResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	w := rc.lookupWaiters[result.Fence.OpID]
	if w == nil || w.will == nil {
		return
	}
	delete(rc.lookupWaiters, result.Fence.OpID)
	r.unregisterLookupCancelContext(rc)
	complete := func(err error) { w.future.Complete(Result{Err: err}) }
	if err = w.ctx.Err(); err != nil {
		complete(err)
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch || w.will.writeFence != rc.state.WriteFence {
		complete(ch.ErrStaleMeta)
		return
	}
	if err = r.validateWillReceiptAdmission(w.ctx, rc, w.will.request); err != nil {
		complete(err)
		return
	}
	if result.Err != nil {
		complete(result.Err)
		return
	}
	if result.StoreWillReceipt == nil {
		complete(ch.ErrInvalidConfig)
		return
	}
	proof := result.StoreWillReceipt.Result
	if proof.CommittedThrough != w.will.committedThrough || proof.CommittedThrough > rc.state.HW || !proof.Valid() {
		complete(ch.ErrLogConflict)
		return
	}
	w.future.Complete(Result{WillReceipt: proof})
}
