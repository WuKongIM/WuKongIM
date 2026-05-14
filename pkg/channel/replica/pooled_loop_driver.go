package replica

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

type pooledLoopMessage struct {
	command  *replicaLoopCommand
	event    machineEvent
	queuedAt time.Time
}

type pooledLoopDriver struct {
	replica *replica
	pool    *ExecutionPool

	mu          sync.Mutex
	queue       []pooledLoopMessage
	queued      bool
	closed      bool
	doneCh      chan struct{}
	doneOnce    sync.Once
	mailboxSize int
	turnBudget  int
}

func newPooledLoopDriver(r *replica, cfg ExecutionConfig) *pooledLoopDriver {
	pool := cfg.Pool
	return &pooledLoopDriver{
		replica:     r,
		pool:        pool,
		doneCh:      r.loopDone,
		mailboxSize: pool.mailboxSize(cfg.MailboxSize),
		turnBudget:  pool.turnBudget(cfg.TurnBudget),
	}
}

func (d *pooledLoopDriver) start() {}

func (d *pooledLoopDriver) submitCommand(ctx context.Context, event machineEvent) machineResult {
	r := d.replica
	if r == nil || d.pool == nil {
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
	if err := d.enqueue(ctx, pooledLoopMessage{command: &cmd}); err != nil {
		return machineResult{Err: err}
	}

	return awaitPooledLoopCommandResult(ctx, reply, d.doneCh, r.stopCh)
}

func awaitPooledLoopCommandResult(ctx context.Context, reply <-chan machineResult, done <-chan struct{}, stop <-chan struct{}) machineResult {
	select {
	case result := <-reply:
		return result
	default:
	}

	select {
	case result := <-reply:
		return result
	case <-done:
		select {
		case result := <-reply:
			return result
		default:
		}
		return machineResult{Err: channel.ErrNotLeader}
	case <-stop:
		select {
		case result := <-reply:
			return result
		default:
		}
		return machineResult{Err: channel.ErrNotLeader}
	case <-ctx.Done():
		return machineResult{Err: ctx.Err()}
	}
}

func (d *pooledLoopDriver) submitResult(ctx context.Context, event machineEvent) error {
	r := d.replica
	if r == nil || d.pool == nil {
		return channel.ErrNotLeader
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if r.isClosed() {
		return channel.ErrNotLeader
	}
	return d.enqueue(ctx, pooledLoopMessage{event: event})
}

func (d *pooledLoopDriver) scheduleResult(delay time.Duration, event machineEvent) {
	if d == nil || d.pool == nil {
		return
	}
	d.pool.scheduleResult(d, delay, event)
}

func (d *pooledLoopDriver) done() <-chan struct{} {
	if d == nil {
		return nil
	}
	return d.doneCh
}

func (d *pooledLoopDriver) enqueue(ctx context.Context, msg pooledLoopMessage) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		d.pool.observeEnqueue("closed")
		return channel.ErrNotLeader
	}
	if len(d.queue) >= d.mailboxSize {
		d.pool.observeEnqueue("queue_full")
		return channel.ErrNotReady
	}
	msg.queuedAt = d.pool.cfg.Now()
	d.queue = append(d.queue, msg)
	d.pool.observeEnqueue("ok")
	d.pool.observeQueueDepth()
	if d.queued {
		return nil
	}
	d.queued = true
	select {
	case d.pool.ready <- d:
		d.pool.observeQueueDepth()
		return nil
	case <-d.pool.stopCh:
		d.queued = false
		d.queue = d.queue[:len(d.queue)-1]
		d.pool.observeEnqueue("closed")
		return channel.ErrNotLeader
	case <-ctx.Done():
		d.queued = false
		d.queue = d.queue[:len(d.queue)-1]
		d.pool.observeEnqueue("canceled")
		return ctx.Err()
	default:
		d.queued = false
		d.queue = d.queue[:len(d.queue)-1]
		d.pool.observeEnqueue("queue_full")
		return channel.ErrNotReady
	}
}

func (d *pooledLoopDriver) drain() {
	processed := 0
	for {
		d.mu.Lock()
		if len(d.queue) == 0 {
			d.queued = false
			closed := d.closed
			d.mu.Unlock()
			if closed {
				d.closeDone()
			}
			return
		}
		msg := d.queue[0]
		copy(d.queue, d.queue[1:])
		d.queue = d.queue[:len(d.queue)-1]
		d.mu.Unlock()

		if !msg.queuedAt.IsZero() {
			d.pool.observeMailboxWait(d.pool.cfg.Now().Sub(msg.queuedAt))
		}
		d.process(msg)
		processed++
		if processed < d.turnBudget {
			continue
		}
		processed = 0

		d.mu.Lock()
		if len(d.queue) == 0 {
			d.queued = false
			closed := d.closed
			d.mu.Unlock()
			if closed {
				d.closeDone()
			}
			return
		}
		select {
		case d.pool.ready <- d:
			d.mu.Unlock()
			return
		default:
			d.mu.Unlock()
		}
	}
}

func (d *pooledLoopDriver) process(msg pooledLoopMessage) {
	if msg.command != nil {
		d.replica.handleLoopCommand(*msg.command)
		if _, closing := msg.command.event.(machineCloseCommand); closing {
			d.mu.Lock()
			d.closed = true
			d.mu.Unlock()
		}
		return
	}
	_ = d.replica.applyLoopEvent(msg.event)
}

func (d *pooledLoopDriver) closeDone() {
	d.doneOnce.Do(func() {
		close(d.doneCh)
	})
}
