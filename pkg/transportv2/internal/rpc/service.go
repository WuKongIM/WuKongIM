package rpc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

const serviceStopGrace = 100 * time.Millisecond

// Request is one service invocation owned by the service after a successful Enqueue.
type Request struct {
	// Payload carries the request bytes and must be released by the service owner.
	Payload core.OwnedBuffer
	// Reply optionally receives a copied response payload and terminal handler error.
	Reply chan Response
}

// Service owns a bounded worker pool for a registered transport service.
type Service struct {
	// ID is the registered service identifier.
	ID uint16

	// handler processes each dequeued request payload.
	handler core.Handler
	// opts stores normalized service limits used by enqueue and workers.
	opts core.ServiceOptions

	// ctx is canceled by Stop to interrupt workers and cooperative handlers.
	ctx context.Context
	// cancel stops the service root context.
	cancel context.CancelFunc

	// mu protects stopped and queuedBytes while Stop races with Enqueue and workers.
	mu sync.Mutex
	// stopped rejects new requests and causes workers to release late dequeues.
	stopped bool
	// queuedBytes is the byte cost currently waiting in queue.
	queuedBytes int64
	// queue stores requests waiting for a worker.
	queue chan Request

	// stopOnce makes Stop idempotent.
	stopOnce sync.Once
	// wg waits for worker goroutines to exit.
	wg sync.WaitGroup
	// done closes after all workers exit.
	done chan struct{}
}

// NewService starts a bounded worker pool for handler.
func NewService(id uint16, handler core.Handler, opts core.ServiceOptions) *Service {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1
	}
	if opts.MaxQueueBytes <= 0 {
		opts.MaxQueueBytes = 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		ID:      id,
		handler: handler,
		opts:    opts,
		ctx:     ctx,
		cancel:  cancel,
		queue:   make(chan Request, opts.QueueSize),
		done:    make(chan struct{}),
	}
	for i := 0; i < opts.Concurrency; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	go func() {
		s.wg.Wait()
		close(s.done)
	}()
	return s
}

// Enqueue transfers payload ownership to the service when it succeeds.
func (s *Service) Enqueue(req Request) error {
	payloadLen := req.Payload.Len()
	if s.opts.MaxPayload > 0 && payloadLen > s.opts.MaxPayload {
		req.Payload.Release()
		return core.ErrMsgTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		req.Payload.Release()
		trySendResponse(req.Reply, Response{Err: core.ErrStopped})
		return core.ErrStopped
	}
	if len(s.queue) >= s.opts.QueueSize {
		req.Payload.Release()
		return core.ErrBusy
	}
	if int64(payloadLen) > s.opts.MaxQueueBytes-s.queuedBytes {
		req.Payload.Release()
		return core.ErrBusy
	}

	select {
	case s.queue <- req:
		s.queuedBytes += int64(payloadLen)
		return nil
	default:
		req.Payload.Release()
		return core.ErrBusy
	}
}

// Stop requests worker shutdown, drains queued payloads, and waits only briefly for cooperative handlers.
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cancel()
		for {
			select {
			case req := <-s.queue:
				s.queuedBytes -= int64(req.Payload.Len())
				trySendResponse(req.Reply, Response{Err: core.ErrStopped})
				req.Payload.Release()
			default:
				s.mu.Unlock()
				select {
				case <-s.done:
				case <-time.After(serviceStopGrace):
				}
				return
			}
		}
	})
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case req := <-s.queue:
			if !s.markDequeued(req) {
				trySendResponse(req.Reply, Response{Err: core.ErrStopped})
				req.Payload.Release()
				continue
			}
			s.handle(req)
		}
	}
}

func (s *Service) markDequeued(req Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queuedBytes -= int64(req.Payload.Len())
	return !s.stopped
}

func (s *Service) handle(req Request) {
	defer req.Payload.Release()

	ctx := s.ctx
	cancel := func() {}
	if s.opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.opts.Timeout)
	}
	defer cancel()

	resp, err := s.handler(ctx, req.Payload.Bytes())
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = core.ErrTimeout
	}
	if req.Reply == nil {
		return
	}

	reply := Response{Payload: append([]byte(nil), resp...), Err: err}
	// Reply channels are required to be buffered by callers; non-blocking send keeps Stop from
	// waiting forever when the caller has abandoned a request.
	trySendResponse(req.Reply, reply)
}

func trySendResponse(ch chan Response, resp Response) {
	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}
