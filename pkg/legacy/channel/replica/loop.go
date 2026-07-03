package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

// replicaLoopCommand carries external commands into the replica single-writer loop.
type replicaLoopCommand struct {
	event machineEvent
	reply chan machineResult
}

func (r *replica) handleLoopCommand(cmd replicaLoopCommand) {
	result := r.applyLoopEvent(cmd.event)
	select {
	case cmd.reply <- result:
	default:
	}
}

func (r *replica) submitLoopCommand(ctx context.Context, event machineEvent) machineResult {
	if r == nil || r.loop == nil {
		return machineResult{Err: channel.ErrNotLeader}
	}
	return r.loop.submitCommand(ctx, event)
}

func (r *replica) submitLoopResult(ctx context.Context, event machineEvent) error {
	if r == nil || r.loop == nil {
		return channel.ErrNotLeader
	}
	return r.loop.submitResult(ctx, event)
}

func (r *replica) isClosed() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.closed
}
