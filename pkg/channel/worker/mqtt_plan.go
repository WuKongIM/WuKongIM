package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// StoreMQTTPlanTask binds the read to independently captured reactor HW.
type StoreMQTTPlanTask struct {
	Request          ch.MQTTReplayPlanRequest
	CommittedThrough uint64
}

type StoreMQTTPlanResult struct{ Plan ch.MQTTReplayPlan }

func runStoreMQTTPlan(ctx context.Context, deps Deps, task Task) Result {
	result := Result{Kind: task.Kind, Fence: task.Fence}
	q := task.StoreMQTTPlan
	if q == nil || deps.Stores == nil || !q.Request.Valid() || q.CommittedThrough == 0 {
		return invalidResult(task)
	}
	lease, err := deps.Stores.ChannelStore(task.Fence.ChannelKey, q.Request.ChannelID)
	if err != nil {
		result.Err = err
		return result
	}
	if lease == nil {
		return invalidResult(task)
	}
	defer func() { _ = lease.Close() }()
	reader, ok := lease.(store.MQTTReplayAnchorStateReader)
	if !ok {
		return invalidResult(task)
	}
	if err := lease.StoreCheckpoint(ctx, ch.Checkpoint{HW: q.CommittedThrough}); err != nil {
		result.Err = err
		return result
	}
	state, err := reader.ReadMQTTReplayAnchors(ctx, q.CommittedThrough, ch.CommandID{})
	if err != nil {
		result.Err = err
		return result
	}
	if err = ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	plan := ch.MQTTReplayPlan{Source: state.Source, Anchor: state.Latest, HasAnchor: state.HasLatest}
	if state.HasRequested || state.Requested != (ch.MQTTReplayAnchorProof{}) || state.Source.CommittedThrough != q.CommittedThrough || !plan.ValidFor(q.Request) {
		result.Err = ch.ErrLogConflict
		return result
	}
	result.StoreMQTTPlan = &StoreMQTTPlanResult{Plan: plan}
	return result
}
