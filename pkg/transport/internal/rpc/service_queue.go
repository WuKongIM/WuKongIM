package rpc

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

// requestEntry belongs to the service queue until unlinkLocked transfers it to
// an executor or expiry owner. Link and watcher fields are protected by Service.mu.
type requestEntry struct {
	req          Request
	prev, next   *requestEntry
	queued       bool
	enqueuedAt   time.Time
	queueContext context.Context
	cancelQueue  context.CancelFunc
	stopExpiry   func() bool
}

func (e *requestEntry) stopQueueWatch() {
	if e.stopExpiry != nil {
		e.stopExpiry()
	}
	if e.cancelQueue != nil {
		e.cancelQueue()
	}
}

func (s *Service) appendLocked(e *requestEntry) {
	e.prev = s.tail
	if s.tail != nil {
		s.tail.next = e
	} else {
		s.head = e
	}
	s.tail = e
	s.queuedItems++
	s.queuedBytes += e.req.retainedBytes
	s.queueRevision = core.NextStateRevision()
}

func (s *Service) unlinkLocked(e *requestEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		s.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		s.tail = e.prev
	}
	e.prev = nil
	e.next = nil
	e.queued = false
	s.queuedItems--
	s.queuedBytes -= e.req.retainedBytes
	s.queueRevision = core.NextStateRevision()
}

// expire releases both the FIFO position and payload even while all workers are blocked.
func (s *Service) expire(e *requestEntry) {
	s.mu.Lock()
	if !e.queued {
		s.mu.Unlock()
		return
	}
	s.unlinkLocked(e)
	err := requestError(e.queueContext.Err())
	if s.stopped {
		err = core.ErrStopped
	}
	e.stopQueueWatch()
	event := s.queueEvent("expired", s.queueSnapshotLocked())
	s.mu.Unlock()
	s.observe(event)
	deliver(e.req, Response{Err: err})
	s.releaseRequest(e.req)
}

func (s *Service) nextRequest() (Request, bool) {
	for {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return Request{}, false
		}
		e := s.head
		if e != nil {
			s.unlinkLocked(e)
			err := e.queueContext.Err()
			e.stopQueueWatch()
			event := s.queueEvent("ok", s.queueSnapshotLocked())
			s.mu.Unlock()
			s.observe(event)
			if err != nil {
				deliver(e.req, Response{Err: requestError(err)})
				s.releaseRequest(e.req)
				continue
			}
			return e.req, true
		}
		s.mu.Unlock()
		select {
		case <-s.ctx.Done():
			return Request{}, false
		case <-s.queueReady:
		}
	}
}

func requestError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return core.ErrTimeout
	}
	if errors.Is(err, context.Canceled) {
		return core.ErrCanceled
	}
	return err
}
