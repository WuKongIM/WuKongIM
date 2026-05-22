package channelplane

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
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
	cells    map[channel.ChannelKey]*channelCell
}

func newReactor(plane *Plane, index int, inboxSize int) *reactor {
	return &reactor{
		plane: plane,
		index: index,
		inbox: make(chan reactorEvent, inboxSize),
		stopc: make(chan struct{}),
		done:  make(chan struct{}),
		cells: make(map[channel.ChannelKey]*channelCell),
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
	if event.completion.cmd != nil {
		event.completion.cmd.future.complete(channel.AppendBatchResult{}, ErrClosed)
	}
}

func (r *reactor) run() {
	defer close(r.done)
	for {
		select {
		case event := <-r.inbox:
			r.handle(event)
			r.drainReady(64)
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
		key := channelhandler.KeyFromChannelID(event.cmd.req.ChannelID)
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
	}
}

func (r *reactor) markReady(key channel.ChannelKey) {
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

func (r *reactor) cell(key channel.ChannelKey) *channelCell {
	cell := r.cells[key]
	if cell == nil {
		cell = newChannelCell(r, key)
		r.cells[key] = cell
	}
	return cell
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
		if event.cmd != nil {
			event.cmd.future.complete(channel.AppendBatchResult{}, err)
		}
	case reactorEventResolveComplete, reactorEventAppendComplete:
		if event.completion.cmd != nil {
			event.completion.cmd.future.complete(channel.AppendBatchResult{}, err)
		}
	}
}

func effectContext(cmd *appendCommand) context.Context {
	if cmd.ctx == nil {
		return context.Background()
	}
	return cmd.ctx
}
