package core

import (
	"sync/atomic"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

// authExecutor is a placeholder for bounded async CONNECT authentication execution.
type authExecutor struct {
	// server owns session and gateway state needed by auth tasks.
	server *Server
	// workers is the normalized auth worker count.
	workers int
	// capacity is the normalized maximum admitted auth backlog.
	capacity int
	// queued tracks admitted auth tasks awaiting execution.
	queued atomic.Int64
	// closed prevents new auth admission after shutdown.
	closed atomic.Bool
}

func newAuthExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*authExecutor, error) {
	return &authExecutor{
		server:   s,
		workers:  opts.AsyncAuthWorkers,
		capacity: opts.AsyncAuthQueueCapacity,
	}, nil
}

func (e *authExecutor) submit(asyncAuthTask) bool {
	return false
}

func (e *authExecutor) stop() {
	if e == nil {
		return
	}
	e.closed.Store(true)
}

func (e *authExecutor) depth() int {
	if e == nil {
		return 0
	}
	return int(e.queued.Load())
}

func (e *authExecutor) totalCapacity() int {
	if e == nil {
		return 0
	}
	return e.capacity
}
