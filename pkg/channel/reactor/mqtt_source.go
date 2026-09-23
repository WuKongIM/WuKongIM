package reactor

import (
	"bytes"
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

type mqttSourceWaiter struct {
	request ch.MQTTSourceRequest
	// committedThrough is captured by this reactor, never supplied by a caller.
	committedThrough uint64
}

// validateMQTTSourceCapability prevents the legacy append mode or a local-only
// store from claiming replicated protection. Route identity is checked even if
// the Channel and leader epochs did not change.
func (r *Reactor) validateMQTTSourceCapability(rc *runtimeChannel, req ch.MQTTSourceRequest) error {
	capability, ok := r.cfg.Store.(store.MQTTSourceActivationFactory)
	if !req.Valid() || r.cfg.QuorumLog == nil || !ok || !capability.SupportsMQTTSourceActivation() {
		return ch.ErrInvalidConfig
	}
	if rc == nil || rc.state == nil || rc.state.ID != req.ChannelID ||
		rc.quorumAuthority.ID.FenceVersion != req.ExpectedRouteGeneration {
		return ch.ErrStaleMeta
	}
	return nil
}

func (r *Reactor) validateMQTTSourceControl(rc *runtimeChannel, event Event) error {
	req := event.MQTTSource
	if err := r.validateMQTTSourceCapability(rc, req); err != nil {
		return err
	}
	appendReq := event.Append
	if appendReq.ChannelID != req.ChannelID || appendReq.ExpectedChannelEpoch != req.ExpectedChannelEpoch ||
		appendReq.ExpectedLeaderEpoch != req.ExpectedLeaderEpoch || appendReq.CommitMode != ch.CommitModeQuorum || len(appendReq.Messages) != 1 {
		return ch.ErrInvalidConfig
	}
	m := appendReq.Messages[0]
	if m.MessageID != req.MessageID || m.ServerTimestampMS != req.ServerTimestampMS || !m.SyncOnce || m.RedDot ||
		m.Setting != 0 || m.Expire != 0 || m.FromUID != "" || m.ClientMsgNo != "" || len(m.PublicationMetadata) != 0 ||
		!bytes.Equal(m.Payload, []byte(quorumlog.MQTTSourceActivationPayload)) {
		return ch.ErrInvalidConfig
	}
	return nil
}

func (r *Reactor) validateMQTTSourceRead(ctx context.Context, rc *runtimeChannel, req ch.MQTTSourceRequest) error {
	if err := r.validateMQTTSourceCapability(rc, req); err != nil {
		return err
	}
	if err := r.validateAppendEvent(ctx, rc, Event{Append: ch.AppendBatchRequest{
		ExpectedChannelEpoch: req.ExpectedChannelEpoch, ExpectedLeaderEpoch: req.ExpectedLeaderEpoch,
	}}); err != nil {
		return err
	}
	// A guard may synchronously cancel the request while returning no denial.
	return ctx.Err()
}

func (r *Reactor) handleMQTTSource(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	ctx := event.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.validateMQTTSourceRead(ctx, rc, event.MQTTSource); err != nil {
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
	rc.lookupWaiters[opID].source = &mqttSourceWaiter{request: event.MQTTSource, committedThrough: rc.state.HW}
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if r.cfg.Pools == nil {
		err = ch.ErrInvalidConfig
	} else {
		err = r.cfg.Pools.Submit(ctx, worker.Task{Kind: worker.TaskStoreMQTTSource, Context: ctx, Fence: fence,
			StoreMQTTSource: &worker.StoreMQTTSourceTask{ChannelID: rc.state.ID, CommittedThrough: rc.state.HW}})
	}
	if err != nil {
		delete(rc.lookupWaiters, opID)
		r.unregisterLookupCancelContext(rc)
		event.Future.Complete(Result{Err: err})
	}
}

func (r *Reactor) handleStoreMQTTSourceResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	waiter := rc.lookupWaiters[result.Fence.OpID]
	if waiter == nil || waiter.source == nil {
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
	if err := r.validateMQTTSourceRead(waiter.ctx, rc, waiter.source.request); err != nil {
		complete(err)
		return
	}
	if result.Err != nil {
		complete(result.Err)
		return
	}
	if result.StoreMQTTSource == nil {
		complete(ch.ErrInvalidConfig)
		return
	}
	stored := result.StoreMQTTSource
	if stored.Found && (stored.Snapshot.Generation == "" || stored.Snapshot.CommittedThrough != waiter.source.committedThrough ||
		stored.Snapshot.CommittedThrough > rc.state.HW || stored.Snapshot.StartAfter >= stored.Snapshot.CommittedThrough) {
		complete(ch.ErrLogConflict)
		return
	}
	waiter.future.Complete(Result{MQTTSource: stored.Snapshot, MQTTSourceFound: stored.Found})
}
