package replica

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type replicaLoopDriver interface {
	start()
	submitCommand(context.Context, machineEvent) machineResult
	submitResult(context.Context, machineEvent) error
	scheduleResult(time.Duration, machineEvent)
	done() <-chan struct{}
}

type dedicatedLoopDriver struct {
	replica *replica
}

func newDedicatedLoopDriver(r *replica) *dedicatedLoopDriver {
	return &dedicatedLoopDriver{replica: r}
}

func (d *dedicatedLoopDriver) start() {
	r := d.replica
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

func (d *dedicatedLoopDriver) submitCommand(ctx context.Context, event machineEvent) machineResult {
	r := d.replica
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

func (d *dedicatedLoopDriver) submitResult(ctx context.Context, event machineEvent) error {
	r := d.replica
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

func (d *dedicatedLoopDriver) scheduleResult(delay time.Duration, event machineEvent) {
	if delay <= 0 {
		delay = time.Millisecond
	}
	time.AfterFunc(delay, func() {
		_ = d.submitResult(context.Background(), event)
	})
}

func (d *dedicatedLoopDriver) done() <-chan struct{} {
	if d == nil || d.replica == nil {
		return nil
	}
	return d.replica.loopDone
}
