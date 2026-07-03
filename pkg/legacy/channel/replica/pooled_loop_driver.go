package replica

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

const pooledLoopInitialMailboxCapacity = 16

type pooledLoopMessage struct {
	command    replicaLoopCommand
	event      machineEvent
	queuedAt   time.Time
	hasCommand bool
}

type pooledLoopDriver struct {
	replica *replica
	pool    *ExecutionPool

	mu          sync.Mutex
	queue       []pooledLoopMessage
	head        int
	tail        int
	size        int
	queued      bool
	closed      bool
	doneCh      chan struct{}
	doneOnce    sync.Once
	mailboxSize int
	turnBudget  int
}

func newPooledLoopDriver(r *replica, cfg ExecutionConfig) *pooledLoopDriver {
	pool := cfg.Pool
	mailboxSize := pool.mailboxSize(cfg.MailboxSize)
	if mailboxSize <= 0 {
		mailboxSize = 1
	}
	return &pooledLoopDriver{
		replica:     r,
		pool:        pool,
		doneCh:      r.loopDone,
		mailboxSize: mailboxSize,
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

	reply := acquirePooledLoopReply()
	cmd := replicaLoopCommand{event: event, reply: reply}
	if err := d.enqueue(ctx, pooledLoopMessage{command: cmd, hasCommand: true}); err != nil {
		releasePooledLoopReply(reply)
		return machineResult{Err: err}
	}

	result, reusable := awaitPooledLoopCommandResult(ctx, reply, d.doneCh, r.stopCh)
	if reusable {
		releasePooledLoopReply(reply)
	}
	return result
}

var pooledLoopReplyPool = sync.Pool{
	New: func() any {
		return make(chan machineResult, 1)
	},
}

func acquirePooledLoopReply() chan machineResult {
	return pooledLoopReplyPool.Get().(chan machineResult)
}

func releasePooledLoopReply(reply chan machineResult) {
	if reply == nil {
		return
	}
	select {
	case <-reply:
	default:
	}
	pooledLoopReplyPool.Put(reply)
}

func awaitPooledLoopCommandResult(ctx context.Context, reply <-chan machineResult, done <-chan struct{}, stop <-chan struct{}) (machineResult, bool) {
	select {
	case result := <-reply:
		return result, true
	default:
	}

	select {
	case result := <-reply:
		return result, true
	case <-done:
		select {
		case result := <-reply:
			return result, true
		default:
		}
		return machineResult{Err: channel.ErrNotLeader}, false
	case <-stop:
		select {
		case result := <-reply:
			return result, true
		default:
		}
		return machineResult{Err: channel.ErrNotLeader}, false
	case <-ctx.Done():
		return machineResult{Err: ctx.Err()}, false
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

func (d *pooledLoopDriver) queueLenLocked() int {
	return d.size
}

func (d *pooledLoopDriver) pushLocked(msg pooledLoopMessage) bool {
	if d.mailboxSize <= 0 || d.size >= d.mailboxSize {
		return false
	}
	if len(d.queue) == 0 {
		capacity := pooledLoopInitialMailboxCapacity
		if capacity > d.mailboxSize {
			capacity = d.mailboxSize
		}
		d.queue = make([]pooledLoopMessage, capacity)
	}
	if d.size == len(d.queue) {
		d.growLocked()
	}
	d.queue[d.tail] = msg
	d.tail = (d.tail + 1) % len(d.queue)
	d.size++
	return true
}

func (d *pooledLoopDriver) popLocked() (pooledLoopMessage, bool) {
	if d.size == 0 {
		return pooledLoopMessage{}, false
	}
	msg := d.queue[d.head]
	d.queue[d.head] = pooledLoopMessage{}
	d.head = (d.head + 1) % len(d.queue)
	d.size--
	if d.size == 0 {
		d.head = 0
		d.tail = 0
	}
	return msg, true
}

func (d *pooledLoopDriver) undoLastPushLocked() {
	if d.size == 0 || len(d.queue) == 0 {
		return
	}
	d.tail--
	if d.tail < 0 {
		d.tail = len(d.queue) - 1
	}
	d.queue[d.tail] = pooledLoopMessage{}
	d.size--
	if d.size == 0 {
		d.head = 0
		d.tail = 0
	}
}

func (d *pooledLoopDriver) growLocked() {
	oldCap := len(d.queue)
	if oldCap >= d.mailboxSize {
		return
	}
	newCap := oldCap * 2
	if newCap < 1 {
		newCap = 1
	}
	if newCap > d.mailboxSize {
		newCap = d.mailboxSize
	}
	next := make([]pooledLoopMessage, newCap)
	for idx := 0; idx < d.size; idx++ {
		next[idx] = d.queue[(d.head+idx)%oldCap]
	}
	d.queue = next
	d.head = 0
	d.tail = d.size
}

func (d *pooledLoopDriver) enqueue(ctx context.Context, msg pooledLoopMessage) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		d.pool.observeEnqueue("closed")
		return channel.ErrNotLeader
	}
	if d.size >= d.mailboxSize {
		d.pool.observeEnqueue("queue_full")
		return channel.ErrNotReady
	}
	msg.queuedAt = d.pool.cfg.Now()
	if !d.pushLocked(msg) {
		d.pool.observeEnqueue("queue_full")
		return channel.ErrNotReady
	}
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
		d.undoLastPushLocked()
		d.pool.observeEnqueue("closed")
		return channel.ErrNotLeader
	case <-ctx.Done():
		d.queued = false
		d.undoLastPushLocked()
		d.pool.observeEnqueue("canceled")
		return ctx.Err()
	default:
		d.queued = false
		d.undoLastPushLocked()
		d.pool.observeEnqueue("queue_full")
		return channel.ErrNotReady
	}
}

func (d *pooledLoopDriver) drain() {
	processed := 0
	for {
		d.mu.Lock()
		msg, ok := d.popLocked()
		if !ok {
			d.queued = false
			closed := d.closed
			d.mu.Unlock()
			if closed {
				d.closeDone()
			}
			return
		}
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
		if d.queueLenLocked() == 0 {
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
	if msg.hasCommand {
		d.replica.handleLoopCommand(msg.command)
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
