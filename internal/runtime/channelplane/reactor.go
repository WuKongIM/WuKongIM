package channelplane

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type reactor struct {
	plane *Plane
	index int
	inbox chan reactorEvent
	stopc chan struct{}
	done  chan struct{}
	once  sync.Once

	submitMu sync.Mutex
	stopped  bool
	ready    scheduler
	cells    map[channel.ChannelID]*channelCell
}

func newReactor(plane *Plane, index int, inboxSize int) *reactor {
	return &reactor{
		plane: plane,
		index: index,
		inbox: make(chan reactorEvent, inboxSize),
		stopc: make(chan struct{}),
		done:  make(chan struct{}),
		cells: make(map[channel.ChannelID]*channelCell),
	}
}

func (r *reactor) start() {
	go r.run()
}

func (r *reactor) stop() {
	r.once.Do(func() {
		r.submitMu.Lock()
		r.stopped = true
		r.submitMu.Unlock()
		close(r.stopc)
	})
}

func (r *reactor) submit(ctx context.Context, cmd *appendCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.submitMu.Lock()
	defer r.submitMu.Unlock()
	if r.stopped {
		return ErrClosed
	}
	select {
	case r.inbox <- reactorEvent{kind: reactorEventAppend, cmd: cmd}:
		observeAppendQueued(r.plane.opts.Observer, cmd.req)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrOverloaded
	}
}

func (r *reactor) post(event reactorEvent) {
	r.submitMu.Lock()
	if r.stopped {
		r.submitMu.Unlock()
		r.completePostedAfterStop(event)
		return
	}
	select {
	case r.inbox <- event:
		r.submitMu.Unlock()
	case <-r.stopc:
		r.submitMu.Unlock()
		r.completePostedAfterStop(event)
	}
}

func (r *reactor) completePostedAfterStop(event reactorEvent) {
	if event.completion.cmd != nil && event.completion.cmd.future.complete(channel.AppendBatchResult{}, ErrClosed) {
		observeAppendCompleted(r.plane.opts.Observer, event.completion.cmd.req, event.completion.route, ErrClosed)
	}
}

func (r *reactor) run() {
	var sweepC <-chan time.Time
	var sweepTicker *time.Ticker
	if r.plane.opts.CellIdleTTL > 0 && r.plane.opts.CellSweepEvery > 0 {
		sweepTicker = time.NewTicker(r.plane.opts.CellSweepEvery)
		sweepC = sweepTicker.C
	}
	defer func() {
		if sweepTicker != nil {
			sweepTicker.Stop()
		}
		close(r.done)
	}()
	for {
		select {
		case event := <-r.inbox:
			r.handle(event)
			r.drainReady(64)
		case now := <-sweepC:
			r.sweepIdleCells(now)
		case <-r.stopc:
			r.failAll(ErrClosed)
			r.drainInbox(ErrClosed)
			return
		}
	}
}

func (r *reactor) handle(event reactorEvent) {
	switch event.kind {
	case reactorEventAppend:
		key := event.cmd.req.ChannelID
		cell := r.cell(key)
		if cell.enqueue(event.cmd) {
			r.markReady(key)
		}
	case reactorEventResolveComplete:
		if cell := r.cells[event.completion.key]; cell != nil {
			cell.handleResolveComplete(event.completion)
		}
	case reactorEventAppendComplete:
		if cell := r.cells[event.completion.key]; cell != nil {
			cell.handleAppendComplete(event.completion)
		}
	case reactorEventInspect:
		if event.inspect != nil {
			event.inspect()
		}
	}
}

func (r *reactor) markReady(key channel.ChannelID) {
	r.ready.markReady(key)
}

func (r *reactor) drainReady(limit int) {
	for i := 0; i < limit; i++ {
		key, ok := r.ready.pop()
		if !ok {
			return
		}
		if cell := r.cells[key]; cell != nil {
			cell.tryStart()
		}
	}
}

func (r *reactor) cell(key channel.ChannelID) *channelCell {
	cell := r.cells[key]
	if cell == nil {
		cell = newChannelCell(r, key)
		r.cells[key] = cell
	}
	return cell
}

func (r *reactor) sweepIdleCells(now time.Time) {
	ttl := r.plane.opts.CellIdleTTL
	for key, cell := range r.cells {
		cell.compactCanceledPending()
		if cell.idleExpired(now, ttl) {
			cell.failAll(ErrClosed)
			delete(r.cells, key)
		}
	}
}

func (r *reactor) runOnLoopForTest(fn func()) {
	done := make(chan struct{})
	r.post(reactorEvent{kind: reactorEventInspect, inspect: func() {
		defer close(done)
		if fn != nil {
			fn()
		}
	}})
	<-done
}

func (r *reactor) pendingForTest(id channel.ChannelID) int {
	pending := 0
	r.runOnLoopForTest(func() {
		if cell := r.cells[id]; cell != nil {
			pending = len(cell.pending)
		}
	})
	return pending
}

func (r *reactor) cellCountForTest() int {
	count := 0
	r.runOnLoopForTest(func() {
		count = len(r.cells)
	})
	return count
}

func (r *reactor) sweepIdleCellsForTest(now time.Time) {
	r.runOnLoopForTest(func() {
		r.sweepIdleCells(now)
	})
}

func (r *reactor) failAll(err error) {
	for _, cell := range r.cells {
		cell.failAll(err)
	}
}

func (r *reactor) drainInbox(err error) {
	for {
		select {
		case event := <-r.inbox:
			r.failEvent(event, err)
		default:
			return
		}
	}
}

func (r *reactor) failEvent(event reactorEvent, err error) {
	switch event.kind {
	case reactorEventAppend:
		if event.cmd != nil && event.cmd.future.complete(channel.AppendBatchResult{}, err) {
			observeAppendCompleted(r.plane.opts.Observer, event.cmd.req, ChannelRoute{}, err)
		}
	case reactorEventResolveComplete, reactorEventAppendComplete:
		if event.completion.cmd != nil && event.completion.cmd.future.complete(channel.AppendBatchResult{}, err) {
			observeAppendCompleted(r.plane.opts.Observer, event.completion.cmd.req, event.completion.route, err)
		}
	}
}

func effectContext(cmd *appendCommand) context.Context {
	if cmd.ctx == nil {
		return context.Background()
	}
	return cmd.ctx
}
