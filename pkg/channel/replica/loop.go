package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

// replicaLoopCommand carries external commands into the replica single-writer loop.
type replicaLoopCommand struct {
	event machineEvent
	reply chan machineResult
}

func (r *replica) startLoop() {
	go func() {
		defer close(r.loopDone)
		for {
			select {
			case cmd := <-r.loopCommands:
				r.handleLoopCommand(cmd)
			case event := <-r.loopResults:
				_ = r.applyLoopEvent(event)
			case <-r.stopCh:
				return
			}
		}
	}()
}

func (r *replica) handleLoopCommand(cmd replicaLoopCommand) {
	result := r.applyLoopEvent(cmd.event)
	select {
	case cmd.reply <- result:
	default:
	}
}

func (r *replica) submitLoopCommand(ctx context.Context, event machineEvent) machineResult {
	if r == nil {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if r.loopCommands == nil || r.stopCh == nil {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if r.isClosed() {
		return machineResult{Err: channel.ErrNotLeader}
	}

	reply := make(chan machineResult, 1)
	cmd := replicaLoopCommand{event: event, reply: reply}
	select {
	case r.loopCommands <- cmd:
	case <-r.stopCh:
		return machineResult{Err: channel.ErrNotLeader}
	case <-ctx.Done():
		return machineResult{Err: ctx.Err()}
	}

	select {
	case result := <-reply:
		return result
	case <-r.stopCh:
		return machineResult{Err: channel.ErrNotLeader}
	case <-ctx.Done():
		return machineResult{Err: ctx.Err()}
	}
}

func (r *replica) submitLoopResult(ctx context.Context, event machineEvent) error {
	if r == nil {
		return channel.ErrNotLeader
	}
	if r.loopResults == nil || r.stopCh == nil {
		return channel.ErrNotLeader
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if r.isClosed() {
		return channel.ErrNotLeader
	}

	select {
	case r.loopResults <- event:
		return nil
	case <-r.stopCh:
		return channel.ErrNotLeader
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *replica) isClosed() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.closed
}
