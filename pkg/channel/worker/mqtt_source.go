package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// StoreMQTTSourceTask carries only HW captured by the owning reactor; callers
// must not derive this checkpoint from an uncommitted store frontier.
type StoreMQTTSourceTask struct {
	ChannelID        ch.ChannelID
	CommittedThrough uint64
}

// StoreMQTTSourceResult is local committed evidence, still subject to reactor
// authority and caller-context checks before the facade can return it.
type StoreMQTTSourceResult struct {
	Snapshot ch.MQTTSourceSnapshot
	Found    bool
}

func runStoreMQTTSource(ctx context.Context, deps Deps, task Task) Result {
	result := Result{Kind: task.Kind, Fence: task.Fence}
	if deps.Stores == nil || task.StoreMQTTSource == nil {
		return invalidResult(task)
	}
	payload := task.StoreMQTTSource
	lease, err := deps.Stores.ChannelStore(task.Fence.ChannelKey, payload.ChannelID)
	if err != nil {
		result.Err = err
		return result
	}
	if lease == nil {
		return invalidResult(task)
	}
	defer func() { _ = lease.Close() }()
	reader, ok := lease.(store.MQTTSourceReader)
	if !ok {
		return invalidResult(task)
	}
	if err := lease.StoreCheckpoint(ctx, ch.Checkpoint{HW: payload.CommittedThrough}); err != nil {
		result.Err = err
		return result
	}
	snapshot, found, err := reader.LoadCommittedMQTTSource(ctx, payload.CommittedThrough)
	result.Err = err
	result.StoreMQTTSource = &StoreMQTTSourceResult{Snapshot: snapshot, Found: found}
	return result
}
