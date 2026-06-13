package core

import (
	"sync/atomic"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// sendExecutor is a placeholder for bounded async SEND dispatch execution.
type sendExecutor struct {
	// server owns session and gateway state needed by send tasks.
	server *Server
	// workers is the normalized send worker count.
	workers int
	// capacity is the normalized maximum admitted send backlog.
	capacity int
	// queued tracks admitted send tasks awaiting execution.
	queued atomic.Int64
	// closed prevents new send admission after shutdown.
	closed atomic.Bool
}

func newSendExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*sendExecutor, error) {
	return &sendExecutor{
		server:   s,
		workers:  opts.AsyncSendWorkers,
		capacity: opts.AsyncSendQueueCapacity,
	}, nil
}

func (e *sendExecutor) submit(*sessionState, string, *frame.SendPacket) bool {
	return false
}

func (e *sendExecutor) stop() {
	if e == nil {
		return
	}
	e.closed.Store(true)
}

func (e *sendExecutor) depth() int {
	if e == nil {
		return 0
	}
	return int(e.queued.Load())
}

func (e *sendExecutor) totalCapacity() int {
	if e == nil {
		return 0
	}
	return e.capacity
}
