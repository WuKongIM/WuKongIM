package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// StoreMQTTReplayTask carries caller fences and independently captured reactor HW.
type StoreMQTTReplayTask struct {
	Request          ch.MQTTReplayRequest
	CommittedThrough uint64
}

// StoreMQTTReplayResult retains the captured boundary for completion fencing.
type StoreMQTTReplayResult struct {
	Page             ch.MQTTReplayPage
	CommittedThrough uint64
}

func runStoreMQTTReplay(ctx context.Context, deps Deps, task Task) Result {
	result := Result{Kind: task.Kind, Fence: task.Fence}
	payload := task.StoreMQTTReplay
	if deps.Stores == nil || payload == nil || !payload.Request.Valid() || payload.Request.Range.Through > payload.CommittedThrough {
		return invalidResult(task)
	}
	lease, err := deps.Stores.ChannelStore(task.Fence.ChannelKey, payload.Request.ChannelID)
	if err != nil {
		result.Err = err
		return result
	}
	if lease == nil {
		return invalidResult(task)
	}
	defer func() { _ = lease.Close() }()
	reader, ok := lease.(store.MQTTSourceReader)
	preparer, supported := lease.(store.MQTTReplayPreparer)
	if !ok || !supported {
		return invalidResult(task)
	}
	if err := lease.StoreCheckpoint(ctx, ch.Checkpoint{HW: payload.CommittedThrough}); err != nil {
		result.Err = err
		return result
	}
	source, found, err := reader.LoadCommittedMQTTSource(ctx, payload.CommittedThrough)
	if err != nil {
		result.Err = err
		return result
	}
	if !found || source.Generation != payload.Request.Range.Generation || source.CommittedThrough != payload.CommittedThrough || source.StartAfter >= payload.Request.Range.From {
		result.Err = ch.ErrLogConflict
		return result
	}
	page, err := preparer.PrepareMQTTReplay(ctx, payload.Request.Range)
	if err != nil {
		result.Err = err
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	if !page.ValidFor(payload.Request.Range) || page.Before.StartAfter != source.StartAfter {
		result.Err = ch.ErrLogConflict
		return result
	}
	result.StoreMQTTReplay = &StoreMQTTReplayResult{Page: page, CommittedThrough: payload.CommittedThrough}
	return result
}
