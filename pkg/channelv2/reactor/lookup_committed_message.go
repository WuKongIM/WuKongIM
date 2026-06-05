package reactor

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

type lookupWaiter struct {
	// future completes the committed-message lookup after the store result is fenced.
	future *Future
	// ctx is the caller context used to cancel the waiter before a blocked lookup returns.
	ctx context.Context
	// messageID is the requested durable message id.
	messageID uint64
}

func (r *Reactor) handleLookupCommittedMessage(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc == nil || rc.state == nil || rc.store == nil {
		event.Future.Complete(Result{Err: ch.ErrChannelNotFound})
		return
	}
	if event.MessageID == 0 || rc.state.HW == 0 {
		event.Future.Complete(Result{})
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
	opID := event.OpID
	if opID == 0 {
		opID = r.nextOpID()
	}
	if err := r.registerLookupWaiter(rc, opID, ctx, event.MessageID, event.Future); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreLookupMessage(ctx, rc.state.ID, fence, event.MessageID); err != nil {
		delete(rc.lookupWaiters, opID)
		r.unregisterLookupCancelContext(rc)
		event.Future.Complete(Result{Err: err})
	}
}

func (r *Reactor) registerLookupWaiter(rc *runtimeChannel, opID ch.OpID, ctx context.Context, messageID uint64, future *Future) error {
	if rc.lookupWaiters == nil {
		rc.lookupWaiters = make(map[ch.OpID]*lookupWaiter)
	}
	if _, ok := rc.lookupWaiters[opID]; ok {
		return ch.ErrInvalidConfig
	}
	rc.lookupWaiters[opID] = &lookupWaiter{future: future, ctx: ctx, messageID: messageID}
	r.registerLookupCancelContext(rc, ctx)
	return nil
}

func (r *Reactor) handleStoreLookupMessageResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	waiter := rc.lookupWaiters[result.Fence.OpID]
	if waiter == nil {
		return
	}
	delete(rc.lookupWaiters, result.Fence.OpID)
	r.unregisterLookupCancelContext(rc)
	future := waiter.future
	if future == nil {
		return
	}
	if result.Fence.Generation != rc.state.Generation || result.Fence.Epoch != rc.state.Epoch || result.Fence.LeaderEpoch != rc.state.LeaderEpoch {
		future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if result.Err != nil {
		future.Complete(Result{Err: result.Err})
		return
	}
	if result.StoreLookupMessage == nil {
		future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	if !result.StoreLookupMessage.Found {
		future.Complete(Result{})
		return
	}
	msg := result.StoreLookupMessage.Message
	if msg.MessageSeq == 0 || msg.MessageSeq > rc.state.HW {
		future.Complete(Result{})
		return
	}
	if msg.MessageID == 0 {
		msg.MessageID = waiter.messageID
	}
	if msg.ChannelID == "" {
		msg.ChannelID = rc.state.ID.ID
	}
	if msg.ChannelType == 0 {
		msg.ChannelType = rc.state.ID.Type
	}
	future.Complete(Result{LookupMessage: msg, LookupFound: true})
}
