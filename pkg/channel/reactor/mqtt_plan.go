package reactor

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

type mqttPlanWaiter struct {
	request ch.MQTTReplayPlanRequest
	// committedThrough is captured once on this reactor; later appends cannot expand it.
	committedThrough uint64
}

func (r *Reactor) validateMQTTPlanAdmission(ctx context.Context, rc *runtimeChannel, q ch.MQTTReplayPlanRequest) error {
	if !q.Valid() {
		return ch.ErrInvalidConfig
	}
	capability, ok := r.cfg.Store.(store.MQTTReplayAnchorFactory)
	if !ok || !capability.SupportsMQTTReplayAnchors() {
		return ch.ErrInvalidConfig
	}
	if err := r.validateMQTTLeaderCapability(rc, q.ChannelID, q.ExpectedRouteGeneration); err != nil {
		return err
	}
	if err := r.validateAppendEvent(ctx, rc, Event{Append: ch.AppendBatchRequest{ExpectedChannelEpoch: q.ExpectedChannelEpoch, ExpectedLeaderEpoch: q.ExpectedLeaderEpoch}}); err != nil {
		return err
	}
	if rc.state.HW == 0 {
		return ch.ErrNotReady
	}
	return ctx.Err()
}

func (r *Reactor) handleMQTTPlan(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	ctx := event.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validateMQTTPlanAdmission(ctx, rc, event.MQTTPlan); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	opID := event.OpID
	if opID == 0 {
		opID = r.nextOpID()
	}
	if err := r.registerLookupWaiter(rc, opID, ctx, 0, event.Future); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	rc.lookupWaiters[opID].plan = &mqttPlanWaiter{request: event.MQTTPlan, committedThrough: rc.state.HW}
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if r.cfg.Pools == nil {
		err = ch.ErrInvalidConfig
	} else {
		err = r.cfg.Pools.Submit(ctx, worker.Task{Kind: worker.TaskStoreMQTTPlan, Context: ctx, Fence: fence, StoreMQTTPlan: &worker.StoreMQTTPlanTask{Request: event.MQTTPlan, CommittedThrough: rc.state.HW}})
	}
	if err != nil {
		delete(rc.lookupWaiters, opID)
		r.unregisterLookupCancelContext(rc)
		event.Future.Complete(Result{Err: err})
	}
}

func (r *Reactor) handleStoreMQTTPlanResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	w := rc.lookupWaiters[result.Fence.OpID]
	if w == nil || w.plan == nil {
		return
	}
	delete(rc.lookupWaiters, result.Fence.OpID)
	r.unregisterLookupCancelContext(rc)
	complete := func(err error) { w.future.Complete(Result{Err: err}) }
	if err := w.ctx.Err(); err != nil {
		complete(err)
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch {
		complete(ch.ErrStaleMeta)
		return
	}
	if err := r.validateMQTTPlanAdmission(w.ctx, rc, w.plan.request); err != nil {
		complete(err)
		return
	}
	if result.Err != nil {
		complete(result.Err)
		return
	}
	stored := result.StoreMQTTPlan
	if stored == nil {
		complete(ch.ErrInvalidConfig)
		return
	}
	p := stored.Plan
	if p.Source.CommittedThrough != w.plan.committedThrough || p.Source.CommittedThrough > rc.state.HW || !p.ValidFor(w.plan.request) {
		complete(ch.ErrLogConflict)
		return
	}
	w.future.Complete(Result{MQTTPlan: p})
}
