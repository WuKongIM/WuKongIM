package rpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

const (
	serviceStopGrace        = 100 * time.Millisecond
	serviceSubmitRetryDelay = 10 * time.Microsecond
)

// Request is one service invocation owned by the service after a successful Enqueue.
type Request struct {
	// Context carries the connection lifetime and negotiated caller budget. Nil
	// uses the service lifetime. Running cancellation is opt-in per service.
	Context context.Context
	// Finish removes connection request tracking after an admitted request ends.
	Finish func()
	// entry owns removable queue state and its admission timestamp.
	entry *requestEntry
	// retainedBytes is captured at admission and survives payload release.
	retainedBytes int64
	// Payload carries the request bytes and must be released by the service owner.
	Payload core.OwnedBuffer
	// Reply optionally receives a copied response payload and terminal handler error.
	Reply chan Response
	// RespondBorrowed receives a response whose payload is valid only during the callback.
	// It takes precedence over Reply and must copy or encode bytes before returning.
	// It runs synchronously on the terminal owner goroutine and must not block.
	RespondBorrowed func(Response)
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
	// retainedBytes and retainedItems include queued and executing request owners.
	retainedBytes int64
	retainedItems int
	// queueRevision orders absolute queue snapshots captured under mu.
	queueRevision uint64
	// head and tail form an intrusive FIFO; expired entries can be removed in O(1).
	head, tail *requestEntry
	// queueTimer is reused for the earliest FIFO queue deadline. Service.mu
	// protects rearming; callbacks recheck the current head before expiring it.
	queueTimer *time.Timer
	// queueReady wakes the pump after admission without spawning per-request workers.
	queueReady chan struct{}
	// requestWG joins admitted owners, including concurrent queue expiry callbacks.
	requestWG sync.WaitGroup
	// inflight is the current number of handlers running for this service.
	inflight atomic.Int32
	// inflightStateMu orders physical inflight mutations and their revisions.
	inflightStateMu sync.Mutex
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
		panic(fmt.Sprintf("transport: create private service executor: %v", err))
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
	if opts.MaxRetainedBytes == 0 {
		opts.MaxRetainedBytes = opts.MaxQueueBytes
		if opts.MaxQueueBytes <= math.MaxInt64/2 {
			opts.MaxRetainedBytes *= 2
		}
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
		queueReady:  make(chan struct{}, 1),
		tokens:      make(chan struct{}, opts.Concurrency),
		done:        make(chan struct{}),
	}
	s.pumpWG.Add(1)
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportRPCService, s.pump)
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportRPCService, func() {
		s.pumpWG.Wait()
		s.taskWG.Wait()
		s.requestWG.Wait()
		close(s.done)
	})
	return s
}

// Enqueue transfers payload ownership on success. Failed admission releases the
// payload but leaves error response and Finish responsibility with the caller.
func (s *Service) Enqueue(req Request) error {
	payloadLen := req.Payload.Len()
	req.retainedBytes = int64(req.Payload.RetainedBytes())
	if s.opts.MaxPayload > 0 && payloadLen > s.opts.MaxPayload {
		req.Payload.Release()
		s.observeAdmissionAndQueue("too_large", payloadLen, s.queueSnapshot())
		return core.ErrMsgTooLarge
	}
	if req.Context == nil {
		req.Context = s.ctx
	}
	s.mu.Lock()
	var err error
	result := "ok"
	switch {
	case s.stopped:
		err = core.ErrStopped
		result = "stopped"
	case req.Context.Err() != nil:
		err = requestError(req.Context.Err())
		result = "canceled"
	case s.queuedItems >= s.opts.QueueSize || req.retainedBytes > s.opts.MaxQueueBytes-s.queuedBytes || req.retainedBytes > s.opts.MaxRetainedBytes-s.retainedBytes:
		err = core.ErrBusy
		result = "busy"
	}
	if err != nil {
		snapshot := s.queueSnapshotLocked()
		s.mu.Unlock()
		req.Payload.Release()
		s.observeAdmissionAndQueue(result, payloadLen, snapshot)
		return err
	}
	entry := &requestEntry{serviceTask: serviceTask{service: s, req: req}, queued: true, enqueuedAt: time.Now()}
	entry.req.entry = entry
	s.appendLocked(entry)
	s.retainedBytes += req.retainedBytes
	s.retainedItems++
	s.requestWG.Add(1)
	// Install under the queue lock so cancellation cannot race past ownership transfer.
	entry.watchQueue(s)
	snapshot := s.queueSnapshotLocked()
	retained := s.retainedEventLocked()
	s.mu.Unlock()
	s.observe(retained)
	s.observeAdmissionAndQueue("ok", payloadLen, snapshot)
	select {
	case s.queueReady <- struct{}{}:
	default:
	}
	return nil
}

// Stop rejects admission and releases queued owners before joining active work.
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cancel()
		var drained []Request
		for s.head != nil {
			entry := s.head
			s.unlinkLocked(entry)
			entry.stopQueueWatch()
			drained = append(drained, entry.req)
		}
		event := s.queueEvent("stopped", s.queueSnapshotLocked())
		s.mu.Unlock()
		s.observe(event)
		for _, req := range drained {
			deliver(req, Response{Err: core.ErrStopped})
			s.releaseRequest(req)
		}
		select {
		case <-s.done:
		case <-time.After(serviceStopGrace):
		}
		if s.ownExecutor {
			_ = s.executor.Stop()
		}
	})
}

func (s *Service) pump() {
	defer s.pumpWG.Done()
	for {
		if !s.acquireToken() {
			return
		}
		req, ok := s.nextRequest()
		if !ok {
			s.releaseToken()
			return
		}
		payloadLen := req.Payload.Len()
		s.taskWG.Add(1)
		// The queue owner survives execution and all terminal callbacks; no
		// pooling or reuse can race a queued-expiry callback that already started.
		task := &req.entry.serviceTask
		for {
			err := s.beforeExecution(req)
			if err == nil {
				err = s.executor.Submit(task)
			}
			if err == nil {
				break
			}
			if errors.Is(err, core.ErrBusy) && s.waitSubmitRetry() {
				continue
			}
			if errors.Is(err, core.ErrBusy) {
				err = core.ErrStopped
			}
			s.taskWG.Done()
			s.releaseToken()
			deliver(req, Response{Err: err})
			s.releaseRequest(req)
			s.observeTask(taskResult(err), payloadLen, 0)
			break
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

func (s *Service) waitSubmitRetry() bool {
	timer := time.NewTimer(serviceSubmitRetryDelay)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// beforeExecution preserves the queue budget through shared-executor admission.
func (s *Service) beforeExecution(req Request) error {
	if s.ctx.Err() != nil {
		return core.ErrStopped
	}
	if err := req.Context.Err(); err != nil {
		return requestError(err)
	}
	if s.opts.QueueTimeout > 0 && req.entry != nil && time.Since(req.entry.enqueuedAt) >= s.opts.QueueTimeout {
		return core.ErrTimeout
	}
	return nil
}

func (s *Service) handle(req Request) error {
	// Recheck after executor admission: dispatch can race with caller cancellation.
	if err := s.beforeExecution(req); err != nil {
		deliver(req, Response{Err: err})
		return err
	}
	ctx := s.ctx
	if s.opts.CancelRunning {
		ctx = req.Context
	}
	cancel := func() {}
	if s.opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.opts.Timeout)
	}
	defer cancel()
	// A propagated context belongs to the connection; service shutdown must also stop it.
	var stopService func() bool
	if s.opts.CancelRunning && req.Context != s.ctx {
		var cancelService context.CancelFunc
		ctx, cancelService = context.WithCancel(ctx)
		stopService = context.AfterFunc(s.ctx, cancelService)
		defer func() { stopService(); cancelService() }()
	}
	if req.entry != nil {
		s.observe(core.Event{Name: "service_wait", ServiceID: s.ID, ServiceAlias: s.opts.Alias, Result: "ok", Duration: nonNegativeSince(req.entry.enqueuedAt)})
	}
	resp, err := s.handler(ctx, req.Payload.Bytes())
	if ctx.Err() != nil {
		err = requestError(ctx.Err())
	}
	if req.Reply == nil && req.RespondBorrowed == nil {
		return err
	}

	reply := Response{Payload: resp, Err: err}
	if req.RespondBorrowed == nil {
		// Asynchronous receivers need a copy that outlives request payload release.
		reply.Payload = append([]byte(nil), resp...)
	}
	// Reply channels are required to be buffered by callers; non-blocking send keeps Stop from
	// waiting forever when the caller has abandoned a request. RespondBorrowed runs inline on the worker.
	deliver(req, reply)
	return err
}

// releaseRequest returns an admission only when its payload owner finishes.
func (s *Service) releaseRequest(req Request) {
	defer s.requestWG.Done()
	if req.Finish != nil {
		defer req.Finish()
	}
	req.Payload.Release()
	s.mu.Lock()
	s.retainedBytes -= req.retainedBytes
	s.retainedItems--
	event := s.retainedEventLocked()
	s.mu.Unlock()
	s.observe(event)
}

func (s *Service) retainedEventLocked() core.Event {
	return core.Event{Name: "service_retained", ServiceID: s.ID, ServiceAlias: s.opts.Alias, Result: "ok", Revision: core.NextStateRevision(), Items: s.retainedItems, Bytes: int(s.retainedBytes), BytesCapacity: s.opts.MaxRetainedBytes}
}

type queueSnapshot struct {
	items         int
	capacity      int
	bytes         int64
	bytesCapacity int64
	revision      uint64
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
		revision:      s.queueRevision,
	}
}

func (s *Service) observeAdmissionAndQueue(result string, payloadBytes int, snapshot queueSnapshot) {
	if s.observer == nil {
		return
	}
	s.observer.ObserveTransport(core.Event{
		Name:          "service_admission",
		ServiceID:     s.ID,
		ServiceAlias:  s.opts.Alias,
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
		ServiceAlias:  s.opts.Alias,
		Result:        result,
		Revision:      snapshot.revision,
		Items:         snapshot.items,
		Capacity:      snapshot.capacity,
		Bytes:         int(snapshot.bytes),
		BytesCapacity: snapshot.bytesCapacity,
	}
}

func (s *Service) observeInflight(inflight int, revision uint64) {
	event := core.Event{
		Name:         "service_inflight",
		ServiceID:    s.ID,
		ServiceAlias: s.opts.Alias,
		Result:       "ok",
		Revision:     revision,
		Capacity:     s.opts.Concurrency,
		Inflight:     inflight,
	}
	if s.executor != nil {
		stats := s.executor.Stats()
		event.PoolRunning = stats.Running
		event.PoolCapacity = stats.Capacity
		event.PoolWaiting = stats.Waiting
	}
	s.observe(event)
}

func (s *Service) changeInflight(delta int32) {
	s.inflightStateMu.Lock()
	inflight := int(s.inflight.Add(delta))
	revision := core.NextStateRevision()
	s.inflightStateMu.Unlock()
	s.observeInflight(inflight, revision)
}

func (s *Service) observeTask(result string, payloadBytes int, duration time.Duration) {
	s.observe(core.Event{
		Name:         "service_task",
		ServiceID:    s.ID,
		ServiceAlias: s.opts.Alias,
		Result:       result,
		Bytes:        payloadBytes,
		Duration:     duration,
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

func deliver(req Request, resp Response) {
	if req.RespondBorrowed != nil {
		req.RespondBorrowed(resp)
		return
	}
	trySendResponse(req.Reply, resp)
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
	// Release after panic recovery has delivered its terminal response.
	defer s.releaseRequest(t.req)

	s.changeInflight(1)
	payloadLen := t.req.Payload.Len()
	started := time.Now()
	result := "ok"
	defer func() {
		if recovered := recover(); recovered != nil {
			result = "panic"
			deliver(t.req, Response{
				Err: fmt.Errorf("transport: service handler panic: %v", recovered),
			})
		}
		s.observeTask(result, payloadLen, nonNegativeSince(started))
		s.changeInflight(-1)
		s.releaseToken()
		s.taskWG.Done()
	}()

	err := s.handle(t.req)
	result = taskResult(err)
}
