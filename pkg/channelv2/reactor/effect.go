package reactor

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

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

func (rc *runtimeChannel) addFetchWaiter(opID ch.OpID, future *Future) {
	if rc == nil || future == nil {
		return
	}
	rc.waiters[opID] = future
	if rc.fetchWaiters == nil {
		rc.fetchWaiters = make(map[ch.OpID]struct{})
	}
	rc.fetchWaiters[opID] = struct{}{}
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
		future.Complete(Result{Err: err})
	}
}

func metadataWouldFenceFetch(state *machine.ChannelState, meta ch.Meta) bool {
	if state == nil {
		return false
	}
	role := ch.RoleFollower
	if meta.Leader == state.LocalNode {
		role = ch.RoleLeader
	}
	return state.Epoch != meta.Epoch || state.LeaderEpoch != meta.LeaderEpoch || state.Role != role || state.Status != meta.Status
}
