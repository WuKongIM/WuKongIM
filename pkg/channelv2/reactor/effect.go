package reactor

import (
	"context"
	"errors"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) submitStoreAppend(ctx context.Context, channelID ch.ChannelID, task machine.Task) error {
	if task.StoreAppend == nil || r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	appendTask := task.StoreAppend
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:  worker.TaskStoreAppend,
		Fence: task.Fence,
		StoreAppend: &worker.StoreAppendTask{
			ChannelID: channelID,
			Records:   appendTask.Records,
			Sync:      appendTask.Sync,
		},
	})
}

func (r *Reactor) submitStoreReadCommitted(ctx context.Context, channelID ch.ChannelID, task machine.Task) error {
	if task.ReadCommitted == nil || r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	read := task.ReadCommitted
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:  worker.TaskStoreReadCommitted,
		Fence: task.Fence,
		StoreReadCommitted: &worker.StoreReadCommittedTask{
			ChannelID: channelID,
			FromSeq:   read.FromSeq,
			MaxSeq:    read.MaxSeq,
			Limit:     read.Limit,
			MaxBytes:  read.MaxBytes,
		},
	})
}

func completeReplies(rc *runtimeChannel, replies []machine.Reply, immediate *Future) bool {
	completed := false
	for _, reply := range replies {
		switch reply.Kind {
		case machine.ReplyKindAppend:
			future := immediate
			if future == nil && rc != nil {
				if _, ok := rc.fetchWaiters[reply.OpID]; !ok {
					future = rc.waiters[reply.OpID]
					delete(rc.waiters, reply.OpID)
				}
			}
			if future != nil {
				batch := ch.AppendBatchResult{Items: reply.AppendItems}
				if len(batch.Items) == 0 && reply.Append.MessageSeq > 0 {
					batch.Items = []ch.AppendBatchItemResult{reply.Append}
				}
				future.Complete(Result{AppendBatch: batch, Err: reply.Err})
				completed = true
			}
		case machine.ReplyKindFetch:
			future := immediate
			if future == nil && rc != nil {
				if _, ok := rc.fetchWaiters[reply.OpID]; ok {
					waiter := rc.waiters[reply.OpID]
					future = waiter
					delete(rc.waiters, reply.OpID)
					delete(rc.fetchWaiters, reply.OpID)
				}
			}
			if future != nil {
				future.Complete(Result{Fetch: reply.Fetch, Err: reply.Err})
				completed = true
			}
		}
	}
	return completed
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

func (rc *runtimeChannel) addFetchWaiter(opID ch.OpID, future *Future) error {
	if err := rc.addWaiter(opID, future); err != nil {
		return err
	}
	if rc == nil || future == nil {
		return nil
	}
	if rc.fetchWaiters == nil {
		rc.fetchWaiters = make(map[ch.OpID]struct{})
	}
	rc.fetchWaiters[opID] = struct{}{}
	return nil
}

func (rc *runtimeChannel) removeFetchWaiter(opID ch.OpID) *Future {
	if rc == nil {
		return nil
	}
	future := rc.waiters[opID]
	delete(rc.waiters, opID)
	delete(rc.fetchWaiters, opID)
	return future
}

func (rc *runtimeChannel) completeStaleFetchIfWaiting(opID ch.OpID) {
	if rc == nil {
		return
	}
	if _, ok := rc.fetchWaiters[opID]; !ok {
		return
	}
	future := rc.removeFetchWaiter(opID)
	future.Complete(Result{Err: ch.ErrStaleMeta})
}

func (rc *runtimeChannel) failPendingFetchWaiters(err error) {
	if rc == nil || len(rc.fetchWaiters) == 0 {
		return
	}
	for opID := range rc.fetchWaiters {
		future := rc.removeFetchWaiter(opID)
		if future != nil {
			future.Complete(Result{Err: err})
		}
	}
}

// failPendingAppendWaiters completes append waiters that are fenced by accepted metadata.
func (rc *runtimeChannel) failPendingAppendWaiters(err error) {
	if rc == nil {
		return
	}
	rc.appendQ.failAll(err)
	for opID, future := range rc.waiters {
		if _, ok := rc.fetchWaiters[opID]; ok {
			continue
		}
		delete(rc.waiters, opID)
		if future != nil {
			future.Complete(Result{Err: err})
		}
	}
	rc.appendInflight = nil
	rc.appendStoreBlocked = false
	rc.appendRetryAt = time.Time{}
	if rc.state != nil && rc.state.InflightAppend != nil {
		rc.state.AbortAppendBatchProposal(rc.state.InflightAppend.OpID)
	}
}

func (rc *runtimeChannel) failWaiters(err error) {
	if rc == nil {
		return
	}
	for opID, future := range rc.waiters {
		delete(rc.waiters, opID)
		if future != nil {
			future.Complete(Result{Err: err})
		}
	}
	for opID := range rc.fetchWaiters {
		delete(rc.fetchWaiters, opID)
	}
	rc.appendQ.failAll(err)
	rc.appendInflight = nil
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
	return cfg
}

func (r *Reactor) nextBatchOpID() ch.OpID {
	if r.cfg.NextOpID != nil {
		return r.cfg.NextOpID()
	}
	return ch.OpID(1<<63 + r.nextOp.Add(1))
}

func (r *Reactor) flushDueAppends(now time.Time) {
	for _, rc := range r.channels {
		r.tryFlushAppend(rc, now)
	}
}

func (r *Reactor) tryFlushAppend(rc *runtimeChannel, now time.Time) {
	if r == nil || rc == nil {
		return
	}
	r.dropCanceledQueuedAppends(rc)
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
}

func (r *Reactor) dropCanceledQueuedAppends(rc *runtimeChannel) {
	if rc == nil {
		return
	}
	for {
		var canceled appendRequest
		found := false
		for _, req := range rc.appendQ.pending {
			if req.ctx == nil || req.ctx.Err() == nil {
				continue
			}
			canceled = req
			found = true
			break
		}
		if !found {
			break
		}
		removed, ok := rc.appendQ.remove(canceled.opID)
		if !ok {
			continue
		}
		cancelErr := removed.ctx.Err()
		if cancelErr == nil {
			cancelErr = context.Canceled
		}
		delete(rc.waiters, removed.opID)
		if rc.state != nil {
			rc.state.CancelAppendWaiter(removed.opID)
		}
		if removed.future != nil {
			removed.future.Complete(Result{Err: cancelErr})
		}
	}
	if len(rc.appendQ.pending) == 0 {
		rc.appendStoreBlocked = false
		rc.appendRetryAt = time.Time{}
	}
}

func (r *Reactor) failAppendBatch(rc *runtimeChannel, batch appendBatch, err error) {
	for _, req := range batch.requests {
		delete(rc.waiters, req.opID)
		if req.future != nil {
			req.future.Complete(Result{Err: err})
		}
	}
}
