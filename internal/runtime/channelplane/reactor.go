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
	cells map[channel.ChannelKey]*channelCell
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
	r.once.Do(func() { close(r.stopc) })
}

func (r *reactor) submit(ctx context.Context, cmd *appendCommand) error {
	select {
	case r.inbox <- reactorEvent{kind: reactorEventAppend, cmd: cmd}:
		observeAppendQueued(r.plane.opts.Observer, cmd.req)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.stopc:
		return ErrClosed
	default:
		return ErrOverloaded
	}
}

func (r *reactor) post(event reactorEvent) {
	select {
	case r.inbox <- event:
	case <-r.stopc:
		if event.completion.cmd != nil {
			event.completion.cmd.future.complete(channel.AppendBatchResult{}, ErrClosed)
		}
	}
}

func (r *reactor) run() {
	defer close(r.done)
	for {
		select {
		case event := <-r.inbox:
			r.handle(event)
		case <-r.stopc:
			r.failAll(ErrClosed)
			return
		}
	}
}

func (r *reactor) handle(event reactorEvent) {
	switch event.kind {
	case reactorEventAppend:
		key := channelhandler.KeyFromChannelID(event.cmd.req.ChannelID)
		cell := r.cell(key)
		cell.enqueue(event.cmd)
		cell.tryStart()
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

func effectContext(cmd *appendCommand) context.Context {
	if cmd.ctx == nil {
		return context.Background()
	}
	return cmd.ctx
}
