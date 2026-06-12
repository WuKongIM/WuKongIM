package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

const (
	rpcBatchMaxItems         = 2
	rpcBatchMaxWait          = 250 * time.Microsecond
	storeAppendBatchMaxItems = 64
	storeAppendBatchMaxWait  = 250 * time.Microsecond
	storeApplyBatchMaxItems  = 64
	storeApplyBatchMaxWait   = 250 * time.Microsecond
)

// QueueObserver receives bounded worker queue depth samples.
type QueueObserver interface {
	SetWorkerQueueDepth(pool string, depth int)
}

// InflightObserver receives current and peak running worker counts.
type InflightObserver interface {
	SetWorkerInflight(pool string, inflight int)
	SetWorkerInflightPeak(pool string, peak int)
}

// QueueCapacityObserver receives configured worker queue capacity and worker count.
// Implementations are called synchronously from pool paths and should be concurrency-safe and non-blocking.
type QueueCapacityObserver interface {
	SetWorkerQueueCapacity(pool string, capacity int)
	SetWorkerWorkers(pool string, workers int)
}

// AdmissionObserver receives bounded worker enqueue outcomes.
// Implementations are called synchronously from Submit and should be concurrency-safe and non-blocking.
type AdmissionObserver interface {
	ObserveWorkerAdmission(pool string, result string)
}

// WaitObserver receives queue wait time for accepted worker tasks.
// Implementations are called synchronously from worker goroutines and should be concurrency-safe and non-blocking.
type WaitObserver interface {
	ObserveWorkerWait(pool string, kind TaskKind, d time.Duration)
}

// TaskObserver receives execution duration for worker tasks with pool context.
// Implementations are called synchronously from worker goroutines and should be concurrency-safe and non-blocking.
type TaskObserver interface {
	ObserveWorkerTask(pool string, kind TaskKind, err error, d time.Duration)
}

// BatchObserver receives worker-side batch observations.
// Implementations are called synchronously from worker goroutines and should be concurrency-safe and non-blocking.
type BatchObserver interface {
	ObserveWorkerBatch(pool string, kind TaskKind, items int, err error)
}

// AntsPoolObserver receives direct ants/v2 executor occupancy samples.
// Implementations are called synchronously from pool paths and should be concurrency-safe and non-blocking.
type AntsPoolObserver interface {
	SetWorkerAntsPoolUsage(pool string, running int, capacity int, waiting int)
}

// queuedTask carries enqueue timing for wait-duration observation.
type queuedTask struct {
	task       Task
	enqueuedAt time.Time
}

type rpcBatchKey struct {
	kind TaskKind
	node ch.NodeID
}

type noopQueueObserver struct{}

func (noopQueueObserver) SetWorkerQueueDepth(pool string, depth int) {}

// PoolConfig defines worker and queue limits for one bounded pool.
type PoolConfig struct {
	Name      string
	Workers   int
	QueueSize int
}

// Pool runs blocking tasks with bounded admission and ants-backed execution.
type Pool struct {
	cfg    PoolConfig
	deps   Deps
	sink   CompletionSink
	queue  chan queuedTask
	slots  chan struct{}
	stop   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	exec   *executor

	obsMu sync.RWMutex
	obs   QueueObserver

	admissionMu  sync.RWMutex
	inflight     atomic.Int64
	inflightPeak atomic.Int64
	once         sync.Once
	closeErr     error
	dispatchWG   sync.WaitGroup
	taskWG       sync.WaitGroup
}

// NewPool starts a bounded worker pool.
func NewPool(cfg PoolConfig, deps Deps, sink CompletionSink) (*Pool, error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || sink == nil {
		return nil, ch.ErrInvalidConfig
	}
	exec, err := newExecutor(cfg.Workers)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		cfg:    cfg,
		deps:   deps,
		sink:   sink,
		queue:  make(chan queuedTask, cfg.QueueSize),
		slots:  make(chan struct{}, cfg.QueueSize),
		stop:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		exec:   exec,
		obs:    noopQueueObserver{},
	}
	p.dispatchWG.Add(1)
	go p.dispatch()
	return p, nil
}

// SetQueueObserver replaces the queue pressure observer; nil restores the no-op observer.
func (p *Pool) SetQueueObserver(observer QueueObserver) {
	if p == nil {
		return
	}
	if observer == nil {
		observer = noopQueueObserver{}
	}
	p.obsMu.Lock()
	p.obs = observer
	p.obsMu.Unlock()
	p.observeQueueCapacity()
	p.observeWorkers()
	p.observeQueueDepth()
	p.observeAntsPool()
}

func (p *Pool) observer() QueueObserver {
	if p == nil {
		return noopQueueObserver{}
	}
	p.obsMu.RLock()
	observer := p.obs
	p.obsMu.RUnlock()
	if observer == nil {
		return noopQueueObserver{}
	}
	return observer
}

// Submit enqueues task or returns a typed backpressure/closed error.
func (p *Pool) Submit(ctx context.Context, task Task) error {
	if p == nil {
		return ch.ErrClosed
	}
	result := "ok"
	defer func() {
		p.observeAdmission(result)
		p.observeQueueDepth()
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.stop:
		result = "closed"
		return ch.ErrClosed
	default:
	}
	select {
	case <-ctx.Done():
		result = workerAdmissionResult(ctx.Err())
		return ctx.Err()
	default:
	}
	p.admissionMu.RLock()
	defer p.admissionMu.RUnlock()
	select {
	case p.slots <- struct{}{}:
	case <-p.stop:
		result = "closed"
		return ch.ErrClosed
	case <-ctx.Done():
		result = workerAdmissionResult(ctx.Err())
		return ctx.Err()
	default:
		result = "full"
		return ch.ErrBackpressured
	}
	queued := queuedTask{task: task, enqueuedAt: time.Now()}
	select {
	case p.queue <- queued:
		return nil
	case <-p.stop:
		p.releaseQueuedSlots(1)
		result = "closed"
		return ch.ErrClosed
	case <-ctx.Done():
		p.releaseQueuedSlots(1)
		result = workerAdmissionResult(ctx.Err())
		return ctx.Err()
	default:
		p.releaseQueuedSlots(1)
		result = "full"
		return ch.ErrBackpressured
	}
}

// Close cancels running tasks, completes queued tasks as closed, and releases the executor.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.once.Do(func() {
		p.cancel()
		close(p.stop)
		p.dispatchWG.Wait()
		p.admissionMu.Lock()
		p.completeQueuedClosed()
		p.admissionMu.Unlock()
		p.taskWG.Wait()
		p.closeErr = p.exec.close()
	})
	return p.closeErr
}

// Name returns the configured pool name.
func (p *Pool) Name() string {
	if p == nil {
		return ""
	}
	return p.cfg.Name
}

// QueueDepth returns the number of tasks waiting in the pool queue.
func (p *Pool) QueueDepth() int {
	if p == nil {
		return 0
	}
	return len(p.slots)
}

func (p *Pool) observeQueueDepth() {
	p.observer().SetWorkerQueueDepth(p.cfg.Name, p.QueueDepth())
}

func (p *Pool) releaseQueuedSlots(count int) {
	for i := 0; i < count; i++ {
		select {
		case <-p.slots:
		default:
			return
		}
	}
}

func (p *Pool) releaseQueuedSlotsAndObserve(count int) {
	p.releaseQueuedSlots(count)
	p.observeQueueDepth()
}

func (p *Pool) observeQueueCapacity() {
	obs, ok := p.observer().(QueueCapacityObserver)
	if !ok {
		return
	}
	obs.SetWorkerQueueCapacity(p.cfg.Name, p.cfg.QueueSize)
}

func (p *Pool) observeWorkers() {
	obs, ok := p.observer().(QueueCapacityObserver)
	if !ok {
		return
	}
	obs.SetWorkerWorkers(p.cfg.Name, p.cfg.Workers)
}

func (p *Pool) observeAntsPool() {
	obs, ok := p.observer().(AntsPoolObserver)
	if !ok {
		return
	}
	capacity := 0
	waiting := 0
	if p.exec != nil {
		capacity = p.exec.capacity()
		waiting = p.exec.waiting()
	}
	// Inflight mirrors occupied execution windows and lets completion samples
	// publish zero before the ants worker goroutine returns to the pool.
	obs.SetWorkerAntsPoolUsage(p.cfg.Name, int(p.inflight.Load()), capacity, waiting)
}

func (p *Pool) observeAdmission(result string) {
	obs, ok := p.observer().(AdmissionObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerAdmission(p.cfg.Name, result)
}

func (p *Pool) observeWait(kind TaskKind, d time.Duration) {
	obs, ok := p.observer().(WaitObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerWait(p.cfg.Name, kind, nonNegativeDuration(d))
}

func (p *Pool) observeTask(kind TaskKind, err error, d time.Duration) {
	obs, ok := p.observer().(TaskObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerTask(p.cfg.Name, kind, err, nonNegativeDuration(d))
}

func (p *Pool) observeBatch(kind TaskKind, items int, err error) {
	obs, ok := p.observer().(BatchObserver)
	if !ok || items <= 0 {
		return
	}
	obs.ObserveWorkerBatch(p.cfg.Name, kind, items, err)
}

func (p *Pool) observeInflight(inflight int) {
	obs, ok := p.observer().(InflightObserver)
	if !ok {
		return
	}
	obs.SetWorkerInflight(p.cfg.Name, inflight)
	peak := p.updateInflightPeak(inflight)
	obs.SetWorkerInflightPeak(p.cfg.Name, peak)
}

func (p *Pool) updateInflightPeak(inflight int) int {
	value := int64(inflight)
	for {
		peak := p.inflightPeak.Load()
		if value <= peak {
			return int(peak)
		}
		if p.inflightPeak.CompareAndSwap(peak, value) {
			return inflight
		}
	}
}

func workerAdmissionResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "other"
	}
}

func nonNegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
