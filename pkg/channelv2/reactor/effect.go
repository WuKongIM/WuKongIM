package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) submitStoreAppend(ctx context.Context, channelID ch.ChannelID, task machine.Task) error {
	if task.StoreAppend == nil || r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	appendTask := task.StoreAppend
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskStoreAppend,
		Fence:   task.Fence,
		Context: ctx,
		StoreAppend: &worker.StoreAppendTask{
			ChannelID: channelID,
			Records:   appendTask.Records,
			Sync:      appendTask.Sync,
		},
	})
}

func (r *Reactor) submitStoreReadLog(ctx context.Context, channelID ch.ChannelID, fence ch.Fence, fromOffset uint64, maxOffset uint64, maxBytes int) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskStoreReadLog,
		Fence:   fence,
		Context: ctx,
		StoreReadLog: &worker.StoreReadLogTask{
			ChannelID:  channelID,
			FromOffset: fromOffset,
			MaxOffset:  maxOffset,
			MaxBytes:   maxBytes,
		},
	})
}

func (r *Reactor) submitStoreApply(ctx context.Context, channelID ch.ChannelID, fence ch.Fence, records []ch.Record, leaderHW uint64) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskStoreApply,
		Fence:   fence,
		Context: ctx,
		StoreApply: &worker.StoreApplyTask{
			ChannelID: channelID,
			Records:   records,
			LeaderHW:  leaderHW,
		},
	})
}

func (r *Reactor) submitStoreCheckpoint(ctx context.Context, channelID ch.ChannelID, fence ch.Fence, checkpoint ch.Checkpoint) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskStoreCheckpoint,
		Fence:   fence,
		Context: ctx,
		StoreCheckpoint: &worker.StoreCheckpointTask{
			ChannelID:  channelID,
			Checkpoint: checkpoint,
		},
	})
}

func (r *Reactor) submitRPCPull(ctx context.Context, leader ch.NodeID, fence ch.Fence, req transport.PullRequest) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskRPCPull,
		Fence:   fence,
		Context: ctx,
		RPCPull: &worker.RPCPullTask{Node: leader, Request: req},
	})
}

func (r *Reactor) submitRPCAck(ctx context.Context, leader ch.NodeID, fence ch.Fence, req transport.AckRequest) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskRPCAck,
		Fence:   fence,
		Context: ctx,
		RPCAck:  &worker.RPCAckTask{Node: leader, Request: req},
	})
}

func (r *Reactor) submitPullHint(ctx context.Context, node ch.NodeID, fence ch.Fence, req transport.PullHintRequest) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskRPCPullHint,
		Fence:   fence,
		Context: ctx,
		RPCPullHint: &worker.RPCPullHintTask{
			Node:    node,
			Request: req,
		},
	})
}

func (r *Reactor) completeReplies(rc *runtimeChannel, replies []machine.Reply, immediate *Future) bool {
	completed := false
	for _, reply := range replies {
		switch reply.Kind {
		case machine.ReplyKindAppend:
			future := immediate
			if future == nil && rc != nil {
				future = rc.waiters[reply.OpID]
				delete(rc.waiters, reply.OpID)
				r.unregisterAppendCancelContext(rc, reply.OpID)
			}
			if future != nil {
				batch := ch.AppendBatchResult{Items: reply.AppendItems}
				if len(batch.Items) == 0 && reply.Append.MessageSeq > 0 {
					batch.Items = []ch.AppendBatchItemResult{reply.Append}
				}
				r.observeAppendComplete(rc, reply.OpID)
				future.Complete(Result{AppendBatch: batch, Err: reply.Err})
				completed = true
			}
		}
	}
	return completed
}

func (r *Reactor) completeAppendFuture(rc *runtimeChannel, opID ch.OpID, future *Future, result Result) {
	r.observeAppendComplete(rc, opID)
	if future != nil {
		future.Complete(result)
	}
}

func (rc *runtimeChannel) addWaiter(opID ch.OpID, future *Future) error {
	if rc == nil || future == nil {
		return nil
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
	if r.appendCancelChannels != nil && rc.state != nil {
		delete(r.appendCancelChannels, rc.state.Key)
	}
}

func (r *Reactor) registerPullCancelContext(rc *runtimeChannel, ctx context.Context) {
	if r == nil || rc == nil || ctx == nil || ctx.Done() == nil {
		return
	}
	if r.pullCancelChannels == nil {
		r.pullCancelChannels = make(map[ch.ChannelKey]*runtimeChannel)
	}
	if rc.state != nil {
		r.pullCancelChannels[rc.state.Key] = rc
	}
}

func (r *Reactor) unregisterPullCancelContext(rc *runtimeChannel) {
	if r == nil || rc == nil || r.pullCancelChannels == nil || rc.state == nil {
		return
	}
	if !rc.hasCancelablePullWaiters() {
		delete(r.pullCancelChannels, rc.state.Key)
	}
}

func (r *Reactor) clearPullCancelChannel(rc *runtimeChannel) {
	if r == nil || rc == nil || r.pullCancelChannels == nil || rc.state == nil {
		return
	}
	delete(r.pullCancelChannels, rc.state.Key)
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
	rc.appendInflight = nil
	rc.failPendingPullWaiters(err)
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
	r.clearAppendCancelContexts(rc)
	r.clearPullCancelChannel(rc)
	if err := rc.store.Close(); err != nil {
		return false
	}
	delete(r.channels, key)
	r.observeChannelRuntimeEvicted(key, role)
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

func (r *Reactor) sweepPullCancellations() {
	if r == nil || len(r.pullCancelChannels) == 0 {
		return
	}
	for key, rc := range r.pullCancelChannels {
		if rc == nil || len(rc.pullWaiters) == 0 {
			delete(r.pullCancelChannels, key)
			continue
		}
		for opID, waiter := range rc.pullWaiters {
			if waiter == nil || waiter.ctx == nil {
				continue
			}
			if err := waiter.ctx.Err(); err != nil {
				r.cancelPullWaiter(rc, opID, err)
			}
		}
		if !rc.hasCancelablePullWaiters() {
			delete(r.pullCancelChannels, key)
		}
	}
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

// metadataWouldFenceState reports whether accepted metadata invalidates pending state.
func metadataWouldFenceState(state *machine.ChannelState, meta ch.Meta) bool {
	if state == nil {
		return false
	}
	role := ch.RoleFollower
	if meta.Leader == state.LocalNode {
		role = ch.RoleLeader
	}
	return state.Epoch != meta.Epoch ||
		state.LeaderEpoch != meta.LeaderEpoch ||
		state.Leader != meta.Leader ||
		state.Role != role ||
		state.Status != meta.Status
}

func defaultReactorConfig(cfg ReactorConfig) ReactorConfig {
	if cfg.MailboxSize <= 0 {
		cfg.MailboxSize = 1024
	}
	if cfg.AppendBatchMaxRecords <= 0 {
		cfg.AppendBatchMaxRecords = 128
	}
	if cfg.AppendBatchMaxBytes <= 0 {
		cfg.AppendBatchMaxBytes = 256 * 1024
	}
	if cfg.AppendBatchMaxWait <= 0 {
		cfg.AppendBatchMaxWait = time.Millisecond
	}
	if cfg.AppendQueueMaxRequests <= 0 {
		cfg.AppendQueueMaxRequests = max(cfg.MailboxSize, 1024)
	}
	if cfg.AppendQueueMaxBytes <= 0 {
		cfg.AppendQueueMaxBytes = 4 * 1024 * 1024
	}
	if cfg.AppendStoreRetryBackoff <= 0 {
		cfg.AppendStoreRetryBackoff = time.Millisecond
	}
	if cfg.ReplicationIdlePollInterval <= 0 {
		cfg.ReplicationIdlePollInterval = 10 * time.Millisecond
	}
	if cfg.ReplicationMinBackoff <= 0 {
		cfg.ReplicationMinBackoff = time.Millisecond
	}
	if cfg.ReplicationMaxBackoff <= 0 {
		cfg.ReplicationMaxBackoff = 100 * time.Millisecond
	}
	if cfg.ReplicationMaxBackoff < cfg.ReplicationMinBackoff {
		cfg.ReplicationMaxBackoff = cfg.ReplicationMinBackoff
	}
	if cfg.PullMaxBytes <= 0 {
		cfg.PullMaxBytes = 64 * 1024
	}
	if cfg.LeaderRecentRecordCacheSize == 0 {
		cfg.LeaderRecentRecordCacheSize = 10
	}
	if cfg.LeaderRecentRecordCacheSize < 0 {
		cfg.LeaderRecentRecordCacheBytes = 0
	} else if cfg.LeaderRecentRecordCacheBytes <= 0 {
		cfg.LeaderRecentRecordCacheBytes = min(cfg.PullMaxBytes, 256*1024)
	}
	if cfg.IdleSlowdownAfter <= 0 {
		cfg.IdleSlowdownAfter = 30 * time.Second
	}
	if cfg.IdleEvictAfter <= 0 {
		cfg.IdleEvictAfter = 5 * time.Minute
	}
	if cfg.IdlePullMinInterval <= 0 {
		cfg.IdlePullMinInterval = cfg.ReplicationIdlePollInterval
	}
	if cfg.IdlePullMaxInterval <= 0 {
		cfg.IdlePullMaxInterval = 5 * time.Second
	}
	if cfg.IdleEvictCheckInterval <= 0 {
		cfg.IdleEvictCheckInterval = time.Second
	}
	if cfg.PullHintRetryInterval <= 0 {
		cfg.PullHintRetryInterval = time.Second
	}
	cfg.Observer = defaultObserver(cfg.Observer)
	return cfg
}

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
	r.sweepAppendCancellationsForChannel(rc)
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
	if len(batch.requests) == 0 {
		rc.appendQ.storeBlocked = false
		return
	}
	waiters := make([]machine.AppendBatchWaiter, 0, len(batch.requests))
	for _, req := range batch.requests {
		waiters = append(waiters, machine.AppendBatchWaiter{OpID: req.opID, CommitMode: req.commitMode, Records: req.records})
	}
	decision := rc.state.ProposeAppendBatch(machine.AppendBatchCommand{BatchOpID: batch.batchOpID, Waiters: waiters})
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
	if err := r.submitStoreAppend(context.Background(), batch.requests[0].req.ChannelID, task); err != nil {
		rc.state.AbortAppendBatchProposal(batch.batchOpID)
		if errors.Is(err, ch.ErrBackpressured) {
			rc.appendQ.restoreFront(batch)
			rc.appendStoreBlocked = true
			rc.appendRetryAt = now.Add(r.cfg.AppendStoreRetryBackoff)
			return
		}
		rc.appendQ.storeBlocked = false
		r.failAppendBatch(rc, batch, err)
		return
	}
	rc.appendInflight = &batch
	rc.appendStoreBlocked = false
	rc.appendRetryAt = time.Time{}
	r.observeAppendBatch(batch, now)
}

func (r *Reactor) sweepAppendCancellations() {
	if r == nil || len(r.appendCancelChannels) == 0 {
		return
	}
	for key, rc := range r.appendCancelChannels {
		if rc == nil || len(rc.appendCancelContexts) == 0 {
			delete(r.appendCancelChannels, key)
			continue
		}
		r.sweepAppendCancellationsForChannel(rc)
		if len(rc.appendCancelContexts) == 0 {
			delete(r.appendCancelChannels, key)
		}
	}
}

func (r *Reactor) sweepAppendCancellationsForChannel(rc *runtimeChannel) {
	if r == nil || rc == nil || len(rc.appendCancelContexts) == 0 {
		return
	}
	for opID, ctx := range rc.appendCancelContexts {
		if ctx == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			r.cancelAppendWaiter(rc, opID, err)
		}
	}
}

func (r *Reactor) cancelAppendWaiter(rc *runtimeChannel, opID ch.OpID, cancelErr error) bool {
	if r == nil || rc == nil {
		return false
	}
	if cancelErr == nil {
		cancelErr = context.Canceled
	}
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
