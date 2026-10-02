package reactor

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

type mqttReplayWaiter struct {
	request ch.MQTTReplayRequest
	// committedThrough is captured by this reactor, never chosen by the caller.
	committedThrough uint64
}

func (r *Reactor) validateMQTTReplayAdmission(ctx context.Context, rc *runtimeChannel, req ch.MQTTReplayRequest) error {
	if !req.Valid() {
		return ch.ErrInvalidConfig
	}
	if err := r.validateMQTTLeaderCapability(rc, req.ChannelID, req.ExpectedRouteGeneration); err != nil {
		return err
	}
	if err := r.validateAppendEvent(ctx, rc, Event{Append: ch.AppendBatchRequest{ExpectedChannelEpoch: req.ExpectedChannelEpoch, ExpectedLeaderEpoch: req.ExpectedLeaderEpoch}}); err != nil {
		return err
	}
	if req.Range.Through > rc.state.HW {
		return ch.ErrLogConflict
	}
	return ctx.Err()
}

func (r *Reactor) handleMQTTReplay(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	ctx := event.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validateMQTTReplayAdmission(ctx, rc, event.MQTTReplay); err != nil {
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
	rc.lookupWaiters[opID].replay = &mqttReplayWaiter{request: event.MQTTReplay, committedThrough: rc.state.HW}
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if r.cfg.Pools == nil {
		err = ch.ErrInvalidConfig
	} else {
		err = r.cfg.Pools.Submit(ctx, worker.Task{Kind: worker.TaskStoreMQTTReplay, Context: ctx, Fence: fence,
			StoreMQTTReplay: &worker.StoreMQTTReplayTask{Request: event.MQTTReplay, CommittedThrough: rc.state.HW}})
	}
	if err != nil {
		delete(rc.lookupWaiters, opID)
		r.unregisterLookupCancelContext(rc)
		event.Future.Complete(Result{Err: err})
	}
}

func (r *Reactor) handleStoreMQTTReplayResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	waiter := rc.lookupWaiters[result.Fence.OpID]
	if waiter == nil || waiter.replay == nil {
		return
	}
	delete(rc.lookupWaiters, result.Fence.OpID)
	r.unregisterLookupCancelContext(rc)
	complete := func(err error) { waiter.future.Complete(Result{Err: err}) }
	if err := waiter.ctx.Err(); err != nil {
		complete(err)
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch {
		complete(ch.ErrStaleMeta)
		return
	}
	if err := r.validateMQTTReplayAdmission(waiter.ctx, rc, waiter.replay.request); err != nil {
		complete(err)
		return
	}
	if result.Err != nil {
		complete(result.Err)
		return
	}
	stored := result.StoreMQTTReplay
	if stored == nil {
		complete(ch.ErrInvalidConfig)
		return
	}
	if stored.CommittedThrough != waiter.replay.committedThrough || stored.CommittedThrough > rc.state.HW || !stored.Page.ValidFor(waiter.replay.request.Range) || stored.Page.After.Through > stored.CommittedThrough {
		complete(ch.ErrLogConflict)
		return
	}
	waiter.future.Complete(Result{MQTTReplay: stored.Page})
}
