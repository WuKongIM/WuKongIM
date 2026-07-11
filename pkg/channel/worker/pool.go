package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

const (
	rpcPullLedBatchMaxItems     = 4
	rpcPullHintLedBatchMaxItems = 2
	rpcBatchMaxWait             = 250 * time.Microsecond
	storeAppendBatchMaxItems    = 64
	storeAppendBatchMaxWait     = 250 * time.Microsecond
	storeApplyBatchMaxItems     = 64
	storeApplyBatchMaxWait      = 250 * time.Microsecond
	workerExecutorStopGrace     = 100 * time.Millisecond
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

// AntsPoolObserver receives workqueue-backed ants/v2 occupancy samples.
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
	Name    string
	Workers int
	// QueueSize bounds accepted tasks waiting for this worker pool.
	QueueSize int
	// BatchMaxWait overrides the pool's default batch coalescing wait. Zero keeps the task-class default.
	BatchMaxWait time.Duration
}

// Pool runs blocking tasks with bounded admission and ants-backed execution.
type Pool struct {
	cfg     PoolConfig
	deps    Deps
	sink    CompletionSink
	runtime *workqueue.BoundedBatchPool[queuedTask]

	obsMu sync.RWMutex
	obs   QueueObserver

	inflight     atomic.Int64
	inflightPeak atomic.Int64
}

// NewPool starts a bounded worker pool.
func NewPool(cfg PoolConfig, deps Deps, sink CompletionSink) (*Pool, error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || sink == nil {
		return nil, ch.ErrInvalidConfig
	}
	p := &Pool{
		cfg:  cfg,
		deps: deps,
		sink: sink,
		obs:  noopQueueObserver{},
	}
	runtime, err := workqueue.NewBoundedBatchPool[queuedTask](workqueue.BoundedBatchPoolConfig[queuedTask]{
		Name:                  cfg.Name,
		Workers:               cfg.Workers,
		QueueSize:             cfg.QueueSize,
		ReleaseTimeout:        workerExecutorStopGrace,
		Observer:              workerWorkqueueObserver{pool: p},
		Policy:                p.batchPolicy,
		CancelAcceptedOnClose: true,
		CancelAccepted:        p.completeQueuedClosed,
		CancelRunningOnClose:  true,
	}, p.runQueuedBatch)
	if errors.Is(err, workqueue.ErrInvalidConfig) {
		return nil, ch.ErrInvalidConfig
	}
	if err != nil {
		return nil, err
	}
	p.runtime = runtime
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
// A nil result for TaskStoreClose transfers its detached store handle to the pool;
// any error leaves ownership with the caller.
func (p *Pool) Submit(ctx context.Context, task Task) error {
	if p == nil {
		return ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if p.runtime == nil {
		return ch.ErrClosed
	}
	if p.runtime.Closed() {
		p.observeAdmission("closed")
		p.observeQueueDepth()
		return ch.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		p.observeAdmission(workerAdmissionResult(err))
		p.observeQueueDepth()
		return err
	}
	if p.runtime.QueueDepth() >= p.runtime.QueueCapacity() {
		p.observeAdmission("full")
		p.observeQueueDepth()
		return ch.ErrBackpressured
	}
	queued := queuedTask{task: task, enqueuedAt: time.Now()}
	err := p.runtime.Submit(ctx, queued)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, workqueue.ErrFull):
		return ch.ErrBackpressured
	case errors.Is(err, workqueue.ErrClosed):
		return ch.ErrClosed
	default:
		return err
	}
}

// Close closes admission, completes queued accepted tasks, cancels running handlers, and waits for exit.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	if p.runtime == nil {
		return nil
	}
	return p.runtime.Close(context.Background())
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
	if p == nil || p.runtime == nil {
		return 0
	}
	return p.runtime.QueueDepth()
}

func (p *Pool) observeQueueDepth() {
	p.observer().SetWorkerQueueDepth(p.cfg.Name, p.QueueDepth())
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
	capacity := 0
	if p != nil && p.runtime != nil {
		capacity = p.runtime.Workers()
	}
	p.observeAntsPoolUsage(int(p.inflight.Load()), capacity, 0)
}

func (p *Pool) observeAntsPoolUsage(running int, capacity int, waiting int) {
	obs, ok := p.observer().(AntsPoolObserver)
	if !ok {
		return
	}
	obs.SetWorkerAntsPoolUsage(p.cfg.Name, running, capacity, waiting)
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

func (p *Pool) batchPolicy(first queuedTask) workqueue.BatchOptions {
	if p == nil {
		return workqueue.BatchOptions{MaxItems: 1}
	}
	switch {
	case first.task.Kind == TaskRPCPull && p.canCollectRPCBatch(first.task):
		return workqueue.BatchOptions{MaxItems: rpcPullLedBatchMaxItems, MaxWait: p.batchMaxWait(rpcBatchMaxWait)}
	case first.task.Kind == TaskRPCPullHint && p.canCollectRPCBatch(first.task):
		return workqueue.BatchOptions{MaxItems: rpcPullHintLedBatchMaxItems, MaxWait: p.batchMaxWait(rpcBatchMaxWait)}
	case p.canCollectStoreAppendBatch(first.task):
		return workqueue.BatchOptions{MaxItems: storeAppendBatchMaxItems, MaxWait: p.batchMaxWait(storeAppendBatchMaxWait)}
	case p.canCollectStoreApplyBatch(first.task):
		return workqueue.BatchOptions{MaxItems: storeApplyBatchMaxItems, MaxWait: p.batchMaxWait(storeApplyBatchMaxWait)}
	default:
		return workqueue.BatchOptions{MaxItems: 1}
	}
}

func (p *Pool) completeQueuedClosed(queued queuedTask, err error) {
	if queued.task.Kind == TaskStoreClose && queued.task.StoreClose != nil {
		_ = queued.task.StoreClose.finalize()
	}
	if p == nil || p.sink == nil {
		return
	}
	if err == nil || errors.Is(err, workqueue.ErrClosed) {
		err = ch.ErrClosed
	}
	p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
	p.observeTask(queued.task.Kind, err, 0)
	p.sink.Complete(Result{Kind: queued.task.Kind, Fence: queued.task.Fence, Err: err})
}

type workerWorkqueueObserver struct {
	pool *Pool
}

func (o workerWorkqueueObserver) ObserveBoundedPool(obs workqueue.BoundedPoolObservation) {
	p := o.pool
	if p == nil {
		return
	}
	switch obs.Kind {
	case "capacity":
		p.observeQueueCapacity()
		p.observeWorkers()
	case "depth":
		p.observer().SetWorkerQueueDepth(p.cfg.Name, obs.QueueDepth)
	case "admission":
		p.observeAdmission(workerAdmissionResultFromWorkqueue(obs.Result))
	case "worker":
		p.observeAntsPoolUsage(obs.Running, obs.Workers, obs.Waiting)
	}
}

func workerAdmissionResultFromWorkqueue(result string) string {
	switch result {
	case "ok", "full", "closed", "canceled", "timeout":
		return result
	default:
		return "other"
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
