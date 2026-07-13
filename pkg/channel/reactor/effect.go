package reactor

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
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

func (r *Reactor) submitStoreLoad(ctx context.Context, channelID ch.ChannelID, fence ch.Fence) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskStoreLoad,
		Fence:   fence,
		Context: ctx,
		StoreLoad: &worker.StoreLoadTask{
			ChannelID: channelID,
		},
	})
}

func (r *Reactor) submitColdStoreLoad(ctx context.Context, channelID ch.ChannelID, fence ch.Fence) error {
	if r.cfg.Pools == nil || r.cfg.Pools.ColdActivation == nil {
		return ch.ErrNotReady
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:      worker.TaskColdStoreLoad,
		Fence:     fence,
		Context:   ctx,
		StoreLoad: &worker.StoreLoadTask{ChannelID: channelID},
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

func (r *Reactor) submitStoreLookupMessage(ctx context.Context, channelID ch.ChannelID, fence ch.Fence, messageID uint64) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskStoreLookupMessage,
		Fence:   fence,
		Context: ctx,
		StoreLookupMessage: &worker.StoreLookupMessageTask{
			ChannelID: channelID,
			MessageID: messageID,
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

func (r *Reactor) submitStoreRetention(ctx context.Context, channelID ch.ChannelID, fence ch.Fence, req ch.RetentionApplyRequest, trimAllowed bool, blockedReason string) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskStoreRetention,
		Fence:   fence,
		Context: ctx,
		StoreRetention: &worker.StoreRetentionTask{
			ChannelID:     channelID,
			ThroughSeq:    req.ThroughSeq,
			TrimAllowed:   trimAllowed,
			BlockedReason: blockedReason,
			Options: store.RetentionTrimOptions{
				MaxMessages: req.Options.MaxTrimMessages,
				MaxBytes:    req.Options.MaxTrimBytes,
			},
		},
	})
}

func (r *Reactor) submitStoreClose(ctx context.Context, fence ch.Fence, cs store.ChannelStore) error {
	if r.cfg.Pools == nil || cs == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskStoreClose,
		Fence:   fence,
		Context: ctx,
		StoreClose: &worker.StoreCloseTask{
			Store: cs,
		},
	})
}

func (r *Reactor) submitRPCPull(ctx context.Context, leader ch.NodeID, fence ch.Fence, req transport.PullRequest, timeout time.Duration) error {
	if r.cfg.Pools == nil {
		return ch.ErrInvalidConfig
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskRPCPull,
		Fence:   fence,
		Context: ctx,
		RPCPull: &worker.RPCPullTask{Node: leader, Request: req, Timeout: timeout},
	})
}

func (r *Reactor) submitMetaResolve(ctx context.Context, fence ch.Fence, id ch.ChannelID) error {
	if r.cfg.Pools == nil || r.cfg.Pools.MetaResolve == nil {
		return ch.ErrNotReady
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:    worker.TaskMetaResolve,
		Fence:   fence,
		Context: ctx,
		MetaResolve: &worker.MetaResolveTask{
			ChannelID: id,
		},
	})
}

func (r *Reactor) submitColdMetaResolve(ctx context.Context, fence ch.Fence, id ch.ChannelID) error {
	if r.cfg.Pools == nil || r.cfg.Pools.ColdActivation == nil {
		return ch.ErrNotReady
	}
	return r.cfg.Pools.Submit(ctx, worker.Task{
		Kind:        worker.TaskColdMetaResolve,
		Fence:       fence,
		Context:     ctx,
		MetaResolve: &worker.MetaResolveTask{ChannelID: id},
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

func (r *Reactor) completeReplies(rc *runtimeChannel, replies []machine.Reply, immediate *Future) int {
	completed := 0
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
				r.observeAppendComplete(rc, reply.OpID, reply.Err)
				future.Complete(Result{AppendBatch: batch, Err: reply.Err})
				completed++
			}
		}
	}
	return completed
}

func (r *Reactor) completeAppendFuture(rc *runtimeChannel, opID ch.OpID, future *Future, result Result) {
	r.observeAppendComplete(rc, opID, result.Err)
	if future != nil {
		future.Complete(result)
	}
}
