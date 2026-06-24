package reactor

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

type retentionWaiter struct {
	// future completes the retention request after the store result is fenced.
	future *Future
	// throughSeq is the requested inclusive compaction boundary.
	throughSeq uint64
	// blockedReason explains why physical deletion was skipped after adoption.
	blockedReason string
}

func (g *Group) RetentionView(ctx context.Context, id ch.ChannelID) (ch.RetentionView, error) {
	if g == nil || g.closed.Load() {
		return ch.RetentionView{}, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key := ch.ChannelKeyForID(id)
	future, err := g.Submit(ctx, key, Event{Kind: EventRetentionView, Key: key})
	if err != nil {
		return ch.RetentionView{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.RetentionView{}, err
	}
	return result.RetentionView, nil
}

func (g *Group) ApplyRetentionBoundary(ctx context.Context, req ch.RetentionApplyRequest) (ch.RetentionApplyResult, error) {
	if g == nil || g.closed.Load() {
		return ch.RetentionApplyResult{}, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req.ChannelID == (ch.ChannelID{}) || req.ThroughSeq == 0 {
		return ch.RetentionApplyResult{}, ch.ErrInvalidConfig
	}
	key := ch.ChannelKeyForID(req.ChannelID)
	future, err := g.Submit(ctx, key, Event{Kind: EventApplyRetentionBoundary, Key: key, Context: ctx, RetentionApply: req})
	if err != nil {
		return ch.RetentionApplyResult{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.RetentionApplyResult{}, err
	}
	return result.RetentionApply, nil
}

func (r *Reactor) handleRetentionView(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	event.Future.Complete(Result{RetentionView: retentionViewFromChannel(rc)})
}

func (r *Reactor) handleApplyRetentionBoundary(event Event) {
	if event.Future == nil {
		event.Future = NewFuture()
	}
	req := event.RetentionApply
	if req.ChannelID == (ch.ChannelID{}) || req.ThroughSeq == 0 {
		event.Future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if rc == nil || rc.state == nil || rc.store == nil {
		event.Future.Complete(Result{Err: ch.ErrChannelNotFound})
		return
	}
	if rc.state.ID != req.ChannelID {
		event.Future.Complete(Result{Err: ch.ErrStaleMeta})
		return
	}
	if req.ThroughSeq > rc.state.RetentionThroughSeq {
		rc.state.RetentionThroughSeq = req.ThroughSeq
	}
	if req.ThroughSeq <= rc.state.LocalRetentionThroughSeq && req.ThroughSeq <= rc.state.PhysicalRetentionThroughSeq {
		event.Future.Complete(Result{RetentionApply: retentionNoopResult(rc.state, req)})
		return
	}
	trimAllowed, blockedReason := retentionTrimDecision(rc.state, req.ThroughSeq)
	opID := event.OpID
	if opID == 0 {
		opID = r.nextOpID()
	}
	if err := r.registerRetentionWaiter(rc, opID, req.ThroughSeq, blockedReason, event.Future); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	ctx := event.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		delete(rc.retentionWaiters, opID)
		event.Future.Complete(Result{Err: err})
		return
	}
	if blockedReason == ch.RetentionBlockedCheckpointLag {
		r.trySubmitRetentionCheckpoint(ctx, rc, req.ThroughSeq)
	}
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreRetention(ctx, rc.state.ID, fence, req, trimAllowed, blockedReason); err != nil {
		delete(rc.retentionWaiters, opID)
		event.Future.Complete(Result{Err: err})
	}
}

func (r *Reactor) trySubmitRetentionCheckpoint(ctx context.Context, rc *runtimeChannel, throughSeq uint64) {
	if r == nil || rc == nil || rc.state == nil || throughSeq == 0 {
		return
	}
	if rc.retentionCheckpointOp != 0 || rc.lifecycle.checkpoint.inflight {
		return
	}
	if throughSeq <= rc.state.CheckpointHW || throughSeq > rc.state.HW || throughSeq > rc.state.LEO {
		return
	}
	opID := r.nextOpID()
	fence := ch.Fence{ChannelKey: rc.state.Key, Generation: rc.state.Generation, Epoch: rc.state.Epoch, LeaderEpoch: rc.state.LeaderEpoch, OpID: opID}
	if err := r.submitStoreCheckpoint(ctx, rc.state.ID, fence, ch.Checkpoint{HW: throughSeq}); err != nil {
		return
	}
	rc.retentionCheckpointOp = opID
}

func (r *Reactor) registerRetentionWaiter(rc *runtimeChannel, opID ch.OpID, throughSeq uint64, blockedReason string, future *Future) error {
	if rc.retentionWaiters == nil {
		rc.retentionWaiters = make(map[ch.OpID]*retentionWaiter)
	}
	if _, ok := rc.retentionWaiters[opID]; ok {
		return ch.ErrInvalidConfig
	}
	rc.retentionWaiters[opID] = &retentionWaiter{future: future, throughSeq: throughSeq, blockedReason: blockedReason}
	return nil
}

func (r *Reactor) handleStoreRetentionResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	waiter := rc.retentionWaiters[result.Fence.OpID]
	if waiter == nil {
		return
	}
	delete(rc.retentionWaiters, result.Fence.OpID)
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
	if result.StoreRetention == nil {
		future.Complete(Result{Err: ch.ErrInvalidConfig})
		return
	}
	stored := result.StoreRetention
	if stored.LocalRetentionThroughSeq > rc.state.LocalRetentionThroughSeq {
		rc.state.LocalRetentionThroughSeq = stored.LocalRetentionThroughSeq
	}
	if stored.PhysicalRetentionThroughSeq > rc.state.PhysicalRetentionThroughSeq {
		rc.state.PhysicalRetentionThroughSeq = stored.PhysicalRetentionThroughSeq
	}
	if stored.RetainedMaxSeq > rc.state.LEO {
		rc.state.LEO = stored.RetainedMaxSeq
	}
	apply := ch.RetentionApplyResult{
		ChannelID:                   rc.state.ID,
		ThroughSeq:                  stored.ThroughSeq,
		LocalRetentionThroughSeq:    rc.state.LocalRetentionThroughSeq,
		PhysicalRetentionThroughSeq: rc.state.PhysicalRetentionThroughSeq,
		DeletedThroughSeq:           stored.DeletedThroughSeq,
		Deleted:                     stored.Deleted,
		More:                        stored.More,
		BlockedReason:               stored.BlockedReason,
	}
	if apply.ThroughSeq == 0 {
		apply.ThroughSeq = waiter.throughSeq
	}
	if apply.BlockedReason == "" {
		apply.BlockedReason = waiter.blockedReason
	}
	future.Complete(Result{RetentionApply: apply})
}

func applyLoadedRetentionState(state *machine.ChannelState, retention store.RetentionState) {
	if state == nil {
		return
	}
	state.LocalRetentionThroughSeq = retention.LocalRetentionThroughSeq
	state.PhysicalRetentionThroughSeq = retention.PhysicalRetentionThroughSeq
	if retention.RetainedMaxSeq > state.LEO {
		state.LEO = retention.RetainedMaxSeq
	}
}

func retentionViewFromChannel(rc *runtimeChannel) ch.RetentionView {
	if rc == nil || rc.state == nil {
		return ch.RetentionView{}
	}
	state := rc.state
	return ch.RetentionView{
		Key:                         state.Key,
		ChannelID:                   state.ID,
		Role:                        state.Role,
		Leader:                      state.Leader,
		Replicas:                    copyNodeIDs(state.Replicas),
		ISR:                         copyNodeIDs(state.ISR),
		RetentionThroughSeq:         state.RetentionThroughSeq,
		LocalRetentionThroughSeq:    state.LocalRetentionThroughSeq,
		PhysicalRetentionThroughSeq: state.PhysicalRetentionThroughSeq,
		LEO:                         state.LEO,
		HW:                          state.HW,
		CheckpointHW:                state.CheckpointHW,
		MinISRMatchOffset:           minISRMatchOffset(state),
	}
}

func retentionTrimDecision(state *machine.ChannelState, throughSeq uint64) (bool, string) {
	if state == nil || throughSeq == 0 {
		return false, ch.RetentionBlockedLEOLag
	}
	if throughSeq <= state.PhysicalRetentionThroughSeq {
		return false, ""
	}
	if throughSeq > state.HW {
		return false, ch.RetentionBlockedHWLag
	}
	if throughSeq > state.CheckpointHW {
		return false, ch.RetentionBlockedCheckpointLag
	}
	if throughSeq > state.LEO {
		return false, ch.RetentionBlockedLEOLag
	}
	if state.Role == ch.RoleLeader && throughSeq > minISRMatchOffset(state) {
		return false, ch.RetentionBlockedMinISRLag
	}
	return true, ""
}

func minISRMatchOffset(state *machine.ChannelState) uint64 {
	if state == nil || len(state.ISR) == 0 {
		return 0
	}
	var min uint64
	for i, node := range state.ISR {
		match := state.RetentionThroughSeq
		if node == state.LocalNode {
			match = state.LEO
		} else if progress, ok := state.Progress[node]; ok {
			match = progress.Match
		}
		if i == 0 || match < min {
			min = match
		}
	}
	return min
}

func retentionNoopResult(state *machine.ChannelState, req ch.RetentionApplyRequest) ch.RetentionApplyResult {
	return ch.RetentionApplyResult{
		ChannelID:                   req.ChannelID,
		ThroughSeq:                  req.ThroughSeq,
		LocalRetentionThroughSeq:    state.LocalRetentionThroughSeq,
		PhysicalRetentionThroughSeq: state.PhysicalRetentionThroughSeq,
	}
}

func (rc *runtimeChannel) failPendingRetentionWaiters(err error) {
	if rc == nil || len(rc.retentionWaiters) == 0 {
		return
	}
	for opID, waiter := range rc.retentionWaiters {
		delete(rc.retentionWaiters, opID)
		if waiter != nil && waiter.future != nil {
			waiter.future.Complete(Result{Err: err})
		}
	}
}

func copyNodeIDs(in []ch.NodeID) []ch.NodeID {
	if len(in) == 0 {
		return nil
	}
	out := make([]ch.NodeID, len(in))
	copy(out, in)
	return out
}
