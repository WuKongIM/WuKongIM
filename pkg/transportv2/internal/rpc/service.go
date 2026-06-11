package rpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

// Service owns a bounded queue and executor-backed pump for a registered transport service.
type Service struct {
	// ID is the registered service identifier.
	ID uint16

	// handler processes each dequeued request payload.
	handler core.Handler
	// opts stores normalized service limits used by enqueue and executor tasks.
	opts core.ServiceOptions
	// observer receives bounded service pressure events.
	observer core.Observer
	// executor runs dequeued handler work for this service.
	executor *Executor
	// ownExecutor reports whether Stop should release executor.
	ownExecutor bool

	// ctx is canceled by Stop to interrupt workers and cooperative handlers.
	ctx context.Context
	// cancel stops the service root context.
	cancel context.CancelFunc

	// mu protects stopped, queuedItems, and queuedBytes while Stop races with Enqueue and pump.
	mu sync.Mutex
	// stopped rejects new requests and causes the pump to release late dequeues.
	stopped bool
	// queuedItems is the item count currently waiting in queue.
	queuedItems int
	// queuedBytes is the byte cost currently waiting in queue.
	queuedBytes int64
	// queue stores requests waiting for a worker.
	queue chan Request
	// inflight is the current number of handlers running for this service.
	inflight atomic.Int32
	// tokens bounds this service's handler concurrency on the shared executor.
	tokens chan struct{}

	// stopOnce makes Stop idempotent.
	stopOnce sync.Once
	// pumpWG waits for the queue pump goroutine to exit.
	pumpWG sync.WaitGroup
	// taskWG waits for executor tasks submitted by the pump to finish.
	taskWG sync.WaitGroup
	// done closes after the pump exits and submitted tasks finish.
	done chan struct{}
}

// NewService starts a service with a private executor sized to service concurrency.
func NewService(id uint16, handler core.Handler, opts core.ServiceOptions, observer core.Observer) *Service {
	opts = normalizeServiceOptions(opts)
	executor, err := NewExecutor(opts.Concurrency, observer)
	if err != nil {
		panic(fmt.Sprintf("transportv2: create private service executor: %v", err))
	}
	return newService(id, handler, opts, observer, executor, true)
}

// NewServiceWithExecutor starts a service that submits handler work to executor.
func NewServiceWithExecutor(id uint16, handler core.Handler, opts core.ServiceOptions, observer core.Observer, executor *Executor) *Service {
	return newService(id, handler, normalizeServiceOptions(opts), observer, executor, false)
}

func normalizeServiceOptions(opts core.ServiceOptions) core.ServiceOptions {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1
	}
	if opts.MaxQueueBytes <= 0 {
		opts.MaxQueueBytes = 1
	}
	return opts
}

func newService(id uint16, handler core.Handler, opts core.ServiceOptions, observer core.Observer, executor *Executor, ownExecutor bool) *Service {
	opts = normalizeServiceOptions(opts)
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		ID:          id,
		handler:     handler,
		opts:        opts,
		observer:    observer,
		executor:    executor,
		ownExecutor: ownExecutor,
		ctx:         ctx,
		cancel:      cancel,
		queue:       make(chan Request, opts.QueueSize),
		tokens:      make(chan struct{}, opts.Concurrency),
		done:        make(chan struct{}),
	}
	s.pumpWG.Add(1)
	go s.pump()
	go func() {
		s.pumpWG.Wait()
		s.taskWG.Wait()
		close(s.done)
	}()
	return s
}

// Enqueue transfers payload ownership to the service when it succeeds.
func (s *Service) Enqueue(req Request) error {
	payloadLen := req.Payload.Len()
	if s.opts.MaxPayload > 0 && payloadLen > s.opts.MaxPayload {
		snapshot := s.queueSnapshot()
		req.Payload.Release()
		s.observeAdmissionAndQueue("too_large", payloadLen, snapshot)
		return core.ErrMsgTooLarge
	}

	s.mu.Lock()

	if s.stopped {
		snapshot := s.queueSnapshotLocked()
		s.mu.Unlock()
		req.Payload.Release()
		trySendResponse(req.Reply, Response{Err: core.ErrStopped})
		s.observeAdmissionAndQueue("stopped", payloadLen, snapshot)
		return core.ErrStopped
	}
	if int64(payloadLen) > s.opts.MaxQueueBytes-s.queuedBytes {
		snapshot := s.queueSnapshotLocked()
		s.mu.Unlock()
		req.Payload.Release()
		s.observeAdmissionAndQueue("busy", payloadLen, snapshot)
		return core.ErrBusy
	}

	select {
	case s.queue <- req:
		s.queuedItems++
		s.queuedBytes += int64(payloadLen)
		snapshot := s.queueSnapshotLocked()
		s.mu.Unlock()
		s.observeAdmissionAndQueue("ok", payloadLen, snapshot)
		return nil
	default:
		snapshot := s.queueSnapshotLocked()
		s.mu.Unlock()
		req.Payload.Release()
		s.observeAdmissionAndQueue("busy", payloadLen, snapshot)
		return core.ErrBusy
	}
}

// Stop requests pump shutdown, drains queued payloads, and waits only briefly for cooperative handlers.
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cancel()
		for {
			select {
			case req := <-s.queue:
				if s.queuedItems > 0 {
					s.queuedItems--
				}
				s.queuedBytes -= int64(req.Payload.Len())
				if s.queuedBytes < 0 {
					s.queuedBytes = 0
				}
				trySendResponse(req.Reply, Response{Err: core.ErrStopped})
				req.Payload.Release()
			default:
				s.mu.Unlock()
				select {
				case <-s.done:
				case <-time.After(serviceStopGrace):
				}
				if s.ownExecutor {
					_ = s.executor.Stop()
				}
				return
			}
		}
	})
}

func (s *Service) pump() {
	defer s.pumpWG.Done()
	for {
		if !s.acquireToken() {
			return
		}
		select {
		case <-s.ctx.Done():
			s.releaseToken()
			return
		case req := <-s.queue:
			active, event := s.markDequeued(req)
			s.observe(event)
			if !active {
				s.releaseToken()
				trySendResponse(req.Reply, Response{Err: core.ErrStopped})
				req.Payload.Release()
				continue
			}
			payloadLen := req.Payload.Len()
			s.taskWG.Add(1)
			if err := s.executor.Submit(&serviceTask{service: s, req: req}); err != nil {
				s.taskWG.Done()
				s.releaseToken()
				trySendResponse(req.Reply, Response{Err: err})
				req.Payload.Release()
				s.observeTask(taskResult(err), payloadLen, 0)
			}
		}
	}
}

func (s *Service) acquireToken() bool {
	select {
	case s.tokens <- struct{}{}:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *Service) releaseToken() {
	select {
	case <-s.tokens:
	default:
	}
}

func (s *Service) markDequeued(req Request) (bool, core.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queuedItems > 0 {
		s.queuedItems--
	}
	s.queuedBytes -= int64(req.Payload.Len())
	if s.queuedBytes < 0 {
		s.queuedBytes = 0
	}
	result := "ok"
	if s.stopped {
		result = "stopped"
	}
	return !s.stopped, s.queueEvent(result, s.queueSnapshotLocked())
}

func (s *Service) handle(req Request) error {
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
		return err
	}

	reply := Response{Payload: append([]byte(nil), resp...), Err: err}
	// Reply channels are required to be buffered by callers; non-blocking send keeps Stop from
	// waiting forever when the caller has abandoned a request.
	trySendResponse(req.Reply, reply)
	return err
}

type queueSnapshot struct {
	items         int
	capacity      int
	bytes         int64
	bytesCapacity int64
}

func (s *Service) queueSnapshot() queueSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queueSnapshotLocked()
}

func (s *Service) queueSnapshotLocked() queueSnapshot {
	items := s.queuedItems
	if items < 0 {
		items = 0
	}
	if items > s.opts.QueueSize {
		items = s.opts.QueueSize
	}
	return queueSnapshot{
		items:         items,
		capacity:      s.opts.QueueSize,
		bytes:         s.queuedBytes,
		bytesCapacity: s.opts.MaxQueueBytes,
	}
}

func (s *Service) observeAdmissionAndQueue(result string, payloadBytes int, snapshot queueSnapshot) {
	if s.observer == nil {
		return
	}
	s.observer.ObserveTransport(core.Event{
		Name:          "service_admission",
		ServiceID:     s.ID,
		Result:        result,
		Items:         snapshot.items,
		Capacity:      snapshot.capacity,
		Bytes:         payloadBytes,
		BytesCapacity: snapshot.bytesCapacity,
	})
	s.observe(s.queueEvent(result, snapshot))
}

func (s *Service) queueEvent(result string, snapshot queueSnapshot) core.Event {
	return core.Event{
		Name:          "service_queue",
		ServiceID:     s.ID,
		Result:        result,
		Items:         snapshot.items,
		Capacity:      snapshot.capacity,
		Bytes:         int(snapshot.bytes),
		BytesCapacity: snapshot.bytesCapacity,
	}
}

func (s *Service) observeInflight(inflight int) {
	s.observe(core.Event{
		Name:      "service_inflight",
		ServiceID: s.ID,
		Result:    "ok",
		Capacity:  s.opts.Concurrency,
		Inflight:  inflight,
	})
}

func (s *Service) observeTask(result string, payloadBytes int, duration time.Duration) {
	s.observe(core.Event{
		Name:      "service_task",
		ServiceID: s.ID,
		Result:    result,
		Bytes:     payloadBytes,
		Duration:  duration,
	})
}

func (s *Service) observe(event core.Event) {
	if s.observer == nil {
		return
	}
	s.observer.ObserveTransport(event)
}

func taskResult(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, core.ErrTimeout) {
		return "timeout"
	}
	return "err"
}

func nonNegativeSince(started time.Time) time.Duration {
	duration := time.Since(started)
	if duration < 0 {
		return 0
	}
	return duration
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

// serviceTask is one executor unit for a service handler invocation.
type serviceTask struct {
	// service owns the task lifecycle and observation state.
	service *Service
	// req is the request payload transferred from the service queue.
	req Request
	// runFunc keeps executor-only tests independent from a full service.
	runFunc func()
}

func (t *serviceTask) run() {
	if t == nil {
		return
	}
	if t.runFunc != nil {
		t.runFunc()
		return
	}
	s := t.service
	if s == nil {
		return
	}

	s.observeInflight(int(s.inflight.Add(1)))
	payloadLen := t.req.Payload.Len()
	started := time.Now()
	result := "ok"
	defer func() {
		if recovered := recover(); recovered != nil {
			result = "panic"
			trySendResponse(t.req.Reply, Response{
				Err: fmt.Errorf("transportv2: service handler panic: %v", recovered),
			})
		}
		s.observeTask(result, payloadLen, nonNegativeSince(started))
		s.observeInflight(int(s.inflight.Add(-1)))
		s.releaseToken()
		s.taskWG.Done()
	}()

	err := s.handle(t.req)
	result = taskResult(err)
}
