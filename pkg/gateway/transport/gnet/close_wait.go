package gnet

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

// physicalCloseReceipt joins submission and callback completion. gnet may
// enqueue a callback before returning a trigger error, so either error sticks.
// Once published, this object remains until its connection is collected.
type physicalCloseReceipt struct {
	mu                            sync.Mutex
	done                          chan struct{}
	submitted, callback, finished bool
	err                           error
}

func (r *physicalCloseReceipt) finish(submission bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	if err != nil {
		r.err = transport.ErrCloseUnproved
	}
	if submission {
		r.submitted = true
	} else {
		r.callback = true
	}
	if r.submitted && (r.callback || r.err != nil) {
		r.finished = true
		close(r.done)
	}
}

// CloseAndWait bypasses the graceful WebSocket close-frame path: the requested
// evidence is physical isolation, not handshake/flush/client receipt. The gnet
// close callback runs after its close operation; OnClose runs before it and is
// deliberately not used as proof. No actor/business callback is joined here.
func (c *stateConn) CloseAndWait(ctx context.Context) error {
	if ctx == nil || c == nil || c.state == nil || c.state.raw == nil {
		return transport.ErrCloseUnproved
	}
	r := c.state.closeReceipt.Load()
	if r == nil {
		candidate := &physicalCloseReceipt{done: make(chan struct{})}
		if c.state.closeReceipt.CompareAndSwap(nil, candidate) {
			r = candidate
			err := c.state.raw.CloseWithCallback(func(_ gnetv2.Conn, err error) error {
				r.finish(false, err)
				return nil
			})
			r.finish(true, err)
		} else {
			r = c.state.closeReceipt.Load()
		}
	}
	select {
	case <-r.done:
		return r.err
	default:
	}
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ transport.CloseWaiter = (*stateConn)(nil)
