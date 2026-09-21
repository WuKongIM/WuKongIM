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
	// serviceTask shares the admitted request with the executor without a second
	// allocation or ownership copy. It is submitted only after FIFO removal.
	serviceTask
	prev, next *requestEntry
	queued     bool
	enqueuedAt time.Time
	// queueTimer is needed only when the queue deadline precedes the caller's.
	queueTimer *time.Timer
	// stopExpiry detaches cancellation from the original request context.
	stopExpiry func() bool
}

// watchQueue avoids a child context and its cancellation tree for a deadline
// used only by the FIFO. Service.mu serializes registration, dequeue, and expiry.
func (e *requestEntry) watchQueue(s *Service) {
	ctx := e.req.Context
	if ctx != s.ctx && ctx.Done() != nil {
		e.stopExpiry = context.AfterFunc(ctx, func() { s.expire(e, requestError(ctx.Err())) })
	}
	if s.opts.QueueTimeout > 0 {
		deadline := e.enqueuedAt.Add(s.opts.QueueTimeout)
		if callerDeadline, ok := ctx.Deadline(); !ok || deadline.Before(callerDeadline) {
			e.queueTimer = time.AfterFunc(time.Until(deadline), func() { s.expire(e, core.ErrTimeout) })
		}
	}
}

func (e *requestEntry) stopQueueWatch() {
	if e.stopExpiry != nil {
		e.stopExpiry()
	}
	if e.queueTimer != nil {
		e.queueTimer.Stop()
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
func (s *Service) expire(e *requestEntry, err error) {
	s.mu.Lock()
	if !e.queued {
		s.mu.Unlock()
		return
	}
	s.unlinkLocked(e)
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
			err := s.beforeExecution(e.req)
			e.stopQueueWatch()
			event := s.queueEvent("ok", s.queueSnapshotLocked())
			s.mu.Unlock()
			s.observe(event)
			if err != nil {
				deliver(e.req, Response{Err: err})
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
