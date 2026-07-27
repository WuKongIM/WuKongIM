package workqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/panjf2000/ants/v2"
)

// BatchOptions describes how one accepted item may collect adjacent peers.
type BatchOptions struct {
	// MaxItems caps the number of items delivered in one handler call.
	MaxItems int
	// MaxWait bounds the wait for one adjacent peer after immediately ready items are drained.
	MaxWait time.Duration
}

// BoundedBatchPolicy chooses batch options from the first item in a batch.
type BoundedBatchPolicy[T any] func(first T) BatchOptions

// BoundedBatchPoolHandler processes one admitted item batch.
type BoundedBatchPoolHandler[T any] func(context.Context, []T) error

// CancelAcceptedFunc observes accepted items canceled by Close before executor entry.
type CancelAcceptedFunc[T any] func(T, error)

// BoundedBatchPoolConfig defines worker, admission, batching, and close behavior.
type BoundedBatchPoolConfig[T any] struct {
	// Name is a stable low-cardinality name used in observations.
	Name string
	// Goroutines receives lifecycle and pool ownership observations.
	Goroutines *goruntimeregistry.Registry
	// Task is the fixed module/task owner of this pool and its auxiliary goroutines.
	Task goruntimeregistry.TaskID
	// Workers is the maximum number of concurrently executing batch handler calls.
	Workers int
	// QueueSize bounds accepted items that have not yet entered the executor.
	QueueSize int
	// ReleaseTimeout bounds graceful ants pool release after Close.
	ReleaseTimeout time.Duration
	// Observer receives low-level pool events. It must be concurrency-safe and non-blocking.
	Observer BoundedPoolObserver
	// Policy chooses batch limits from the first item. Nil means single-item batches.
	Policy BoundedBatchPolicy[T]
	// CancelAcceptedOnClose cancels accepted items before executor entry instead of draining them.
	CancelAcceptedOnClose bool
	// CancelAccepted is called for each item canceled by CancelAcceptedOnClose.
	CancelAccepted CancelAcceptedFunc[T]
	// CancelRunningOnClose cancels the pool runtime context as soon as Close starts.
	CancelRunningOnClose bool
}

// BoundedBatchPool admits work into a bounded queue and executes adjacent batches on an ants pool.
type BoundedBatchPool[T any] struct {
	cfg     BoundedBatchPoolConfig[T]
	handler BoundedBatchPoolHandler[T]

	queue chan boundedBatchPoolTask[T]
	slots chan struct{}
	stop  chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	pool   *ants.PoolWithFuncGeneric[[]boundedBatchPoolTask[T]]

	closed   atomic.Bool
	running  atomic.Int64
	rejected atomic.Int64

	admissionMu    sync.RWMutex
	closeOnce      sync.Once
	closeErr       error
	dispatchWG     sync.WaitGroup
	taskWG         sync.WaitGroup
	unregisterPool func()
}

type boundedBatchPoolTask[T any] struct {
	item       T
	enqueuedAt time.Time
}

// NewBoundedBatchPool starts a bounded worker pool that executes adjacent item batches.
func NewBoundedBatchPool[T any](cfg BoundedBatchPoolConfig[T], handler BoundedBatchPoolHandler[T]) (*BoundedBatchPool[T], error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || handler == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.ReleaseTimeout <= 0 {
		cfg.ReleaseTimeout = defaultReleaseTimeout
	}
	if cfg.Policy == nil {
		cfg.Policy = func(T) BatchOptions {
			return BatchOptions{MaxItems: 1}
		}
	}
	cfg.Goroutines, cfg.Task = normalizeOwnership(cfg.Goroutines, cfg.Task)
	ctx, cancel := context.WithCancel(context.Background())
	p := &BoundedBatchPool[T]{
		cfg:     cfg,
		handler: handler,
		queue:   make(chan boundedBatchPoolTask[T], cfg.QueueSize),
		slots:   make(chan struct{}, cfg.QueueSize),
		stop:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	pool, err := ants.NewPoolWithFuncGeneric[[]boundedBatchPoolTask[T]](
		cfg.Workers,
		p.runBatch,
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(poolPanicHandler(cfg.Goroutines, cfg.Task)),
	)
	if err != nil {
		cancel()
		return nil, err
	}
	p.pool = pool
	unregister, err := cfg.Goroutines.RegisterPool(cfg.Task, p.poolStats)
	if err != nil {
		pool.Release()
		cancel()
		return nil, err
	}
	p.unregisterPool = unregister
	p.observeCapacity()
	p.observeDepth()
	p.dispatchWG.Add(1)
	goruntimeregistry.SafeGo(cfg.Goroutines, cfg.Task, p.dispatch)
	return p, nil
}

// Submit attempts non-blocking bounded admission.
func (p *BoundedBatchPool[T]) Submit(ctx context.Context, item T) error {
	if p == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if p.closed.Load() {
		p.observeAdmission(resultClosed)
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		p.observeAdmission(contextResult(err))
		return err
	}
	if err := p.acquireSlot(ctx); err != nil {
		if errors.Is(err, ErrFull) {
			p.rejected.Add(1)
		}
		p.observeAdmission(errorResult(err))
		return err
	}

	p.admissionMu.RLock()
	defer p.admissionMu.RUnlock()

	if p.closed.Load() {
		p.releaseSlots(1)
		p.observeAdmission(resultClosed)
		p.observeDepth()
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		p.releaseSlots(1)
		p.observeAdmission(contextResult(err))
		p.observeDepth()
		return err
	}
	task := boundedBatchPoolTask[T]{item: item, enqueuedAt: time.Now()}
	select {
	case p.queue <- task:
		p.observeAdmission(resultOK)
		p.observeDepth()
		return nil
	case <-p.stop:
		p.releaseSlots(1)
		p.observeAdmission(resultClosed)
		p.observeDepth()
		return ErrClosed
	case <-ctx.Done():
		p.releaseSlots(1)
		p.observeAdmission(contextResult(ctx.Err()))
		p.observeDepth()
		return ctx.Err()
	default:
		p.releaseSlots(1)
		p.rejected.Add(1)
		p.observeAdmission(resultFull)
		p.observeDepth()
		return ErrFull
	}
}

func (p *BoundedBatchPool[T]) acquireSlot(ctx context.Context) error {
	select {
	case p.slots <- struct{}{}:
		return nil
	case <-p.stop:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrFull
	}
}

// Close closes admission and drains or cancels already accepted work until ctx expires.
func (p *BoundedBatchPool[T]) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.closeOnce.Do(func() {
		p.admissionMu.Lock()
		p.closed.Store(true)
		close(p.stop)
		p.admissionMu.Unlock()
		if p.cfg.CancelRunningOnClose {
			p.cancel()
		}

		done := make(chan struct{})
		goruntimeregistry.SafeGo(p.cfg.Goroutines, p.cfg.Task, func() {
			p.dispatchWG.Wait()
			p.taskWG.Wait()
			close(done)
		})
		select {
		case <-done:
			p.closeErr = nil
		case <-ctx.Done():
			p.cancel()
			p.closeErr = ctx.Err()
		}
		if p.pool != nil {
			releaseOwnedPool(
				p.cfg.Goroutines,
				p.cfg.Task,
				func() error { return p.pool.ReleaseTimeout(p.cfg.ReleaseTimeout) },
				p.pool.Running,
				p.unregisterPool,
			)
		}
		p.cancel()
	})
	return p.closeErr
}

func (p *BoundedBatchPool[T]) poolStats() goruntimeregistry.PoolStats {
	if p == nil {
		return goruntimeregistry.PoolStats{}
	}
	stats := goruntimeregistry.PoolStats{
		BusyTasks:     p.running.Load(),
		Capacity:      int64(p.cfg.Workers),
		QueueDepth:    int64(p.QueueDepth()),
		QueueCapacity: int64(p.cfg.QueueSize),
		RejectedTotal: p.rejected.Load(),
	}
	if p.pool != nil {
		stats.Goroutines = poolGoroutines(p.pool.Running(), p.pool.IsClosed())
	}
	return stats
}

// QueueDepth returns the accepted-but-not-yet-executing item count.
func (p *BoundedBatchPool[T]) QueueDepth() int {
	if p == nil {
		return 0
	}
	return len(p.slots)
}

// Workers returns the configured worker capacity.
func (p *BoundedBatchPool[T]) Workers() int {
	if p == nil {
		return 0
	}
	return p.cfg.Workers
}

// QueueCapacity returns the configured admission capacity.
func (p *BoundedBatchPool[T]) QueueCapacity() int {
	if p == nil {
		return 0
	}
	return p.cfg.QueueSize
}

// Closed reports whether admission has been closed.
func (p *BoundedBatchPool[T]) Closed() bool {
	if p == nil {
		return true
	}
	return p.closed.Load()
}

func (p *BoundedBatchPool[T]) dispatch() {
	defer p.dispatchWG.Done()
	for {
		select {
		case task := <-p.queue:
			p.observeDepth()
			if p.shouldCancelAccepted() {
				p.cancelTasks([]boundedBatchPoolTask[T]{task})
				p.cancelQueued()
				return
			}
			batch := p.collectBatch(task)
			if len(batch) == 0 {
				continue
			}
			if p.shouldCancelAccepted() {
				p.cancelTasks(batch)
				p.cancelQueued()
				return
			}
			if !p.submitToExecutor(batch) {
				return
			}
		case <-p.stop:
			if p.cfg.CancelAcceptedOnClose {
				p.cancelQueued()
				return
			}
			p.drainQueue()
			return
		}
	}
}

func (p *BoundedBatchPool[T]) drainQueue() {
	for {
		select {
		case task := <-p.queue:
			p.observeDepth()
			batch := p.collectBatch(task)
			if len(batch) == 0 {
				continue
			}
			if !p.submitToExecutor(batch) {
				return
			}
		default:
			p.observeDepth()
			return
		}
	}
}

func (p *BoundedBatchPool[T]) collectBatch(first boundedBatchPoolTask[T]) []boundedBatchPoolTask[T] {
	opts := p.cfg.Policy(first.item)
	maxItems := opts.MaxItems
	if maxItems <= 1 {
		return []boundedBatchPoolTask[T]{first}
	}
	if maxItems > p.cfg.QueueSize {
		maxItems = p.cfg.QueueSize
	}
	batch := make([]boundedBatchPoolTask[T], 0, maxItems)
	batch = append(batch, first)
	p.drainReadyInto(&batch, maxItems)
	if len(batch) >= maxItems || opts.MaxWait <= 0 || p.closed.Load() {
		return batch
	}

	timer := time.NewTimer(opts.MaxWait)
	defer timer.Stop()
	select {
	case next := <-p.queue:
		batch = append(batch, next)
		p.observeDepth()
		p.drainReadyInto(&batch, maxItems)
	case <-timer.C:
	case <-p.stop:
	case <-p.ctx.Done():
	}
	p.drainReadyInto(&batch, maxItems)
	return batch
}

func (p *BoundedBatchPool[T]) drainReadyInto(batch *[]boundedBatchPoolTask[T], maxItems int) {
	for len(*batch) < maxItems {
		select {
		case next := <-p.queue:
			*batch = append(*batch, next)
			p.observeDepth()
		default:
			return
		}
	}
}

func (p *BoundedBatchPool[T]) submitToExecutor(batch []boundedBatchPoolTask[T]) bool {
	if len(batch) == 0 {
		return true
	}
	for {
		if p.shouldCancelAccepted() {
			p.cancelTasks(batch)
			return false
		}
		p.extendBatchReady(&batch)
		p.taskWG.Add(1)
		err := p.pool.Invoke(batch)
		if err == nil {
			p.releaseSlots(len(batch))
			p.observeDepth()
			return true
		}
		p.taskWG.Done()
		if errors.Is(err, ants.ErrPoolClosed) {
			if p.shouldCancelAccepted() {
				p.cancelTasks(batch)
			} else {
				p.releaseSlots(len(batch))
				p.observeDepth()
			}
			return false
		}
		if !errors.Is(err, ants.ErrPoolOverload) {
			p.releaseSlots(len(batch))
			p.observeDepth()
			return true
		}
		if !p.retryExecutor(batch) {
			return false
		}
	}
}

func (p *BoundedBatchPool[T]) extendBatchReady(batch *[]boundedBatchPoolTask[T]) {
	if len(*batch) == 0 {
		return
	}
	opts := p.cfg.Policy((*batch)[0].item)
	maxItems := opts.MaxItems
	if maxItems <= len(*batch) {
		return
	}
	if maxItems > p.cfg.QueueSize {
		maxItems = p.cfg.QueueSize
	}
	p.drainReadyInto(batch, maxItems)
}

func (p *BoundedBatchPool[T]) retryExecutor(batch []boundedBatchPoolTask[T]) bool {
	timer := time.NewTimer(executorRetryDelay)
	defer timer.Stop()
	if p.cfg.CancelAcceptedOnClose {
		select {
		case <-timer.C:
			return true
		case <-p.stop:
			p.cancelTasks(batch)
			return false
		case <-p.ctx.Done():
			if p.shouldCancelAccepted() {
				p.cancelTasks(batch)
			} else {
				p.releaseSlots(len(batch))
				p.observeDepth()
			}
			return false
		}
	}
	select {
	case <-timer.C:
		return true
	case <-p.ctx.Done():
		p.releaseSlots(len(batch))
		p.observeDepth()
		return false
	}
}

func (p *BoundedBatchPool[T]) runBatch(batch []boundedBatchPoolTask[T]) {
	defer p.taskWG.Done()
	running := int(p.running.Add(1))
	p.observeWorker(running)
	defer func() {
		running := int(p.running.Add(-1))
		p.observeWorker(running)
	}()
	wait := time.Duration(0)
	if len(batch) > 0 {
		wait = time.Since(batch[0].enqueuedAt)
	}
	p.observeWait(wait)
	items := make([]T, len(batch))
	for i := range batch {
		items[i] = batch[i].item
	}

	started := time.Now()
	var err error
	defer func() {
		if v := recover(); v != nil {
			p.observeTask(resultPanic, nil, time.Since(started))
			panic(v)
		}
		p.observeTask(errorResult(err), err, time.Since(started))
	}()
	err = p.handler(p.ctx, items)
}

func (p *BoundedBatchPool[T]) shouldCancelAccepted() bool {
	return p.cfg.CancelAcceptedOnClose && p.closed.Load()
}

func (p *BoundedBatchPool[T]) cancelQueued() {
	for {
		select {
		case task := <-p.queue:
			p.cancelTasks([]boundedBatchPoolTask[T]{task})
		default:
			p.observeDepth()
			return
		}
	}
}

func (p *BoundedBatchPool[T]) cancelTasks(tasks []boundedBatchPoolTask[T]) {
	if len(tasks) == 0 {
		return
	}
	p.releaseSlots(len(tasks))
	p.observeDepth()
	if p.cfg.CancelAccepted == nil {
		return
	}
	for _, task := range tasks {
		p.cfg.CancelAccepted(task.item, ErrClosed)
	}
}

func (p *BoundedBatchPool[T]) releaseSlots(count int) {
	for i := 0; i < count; i++ {
		select {
		case <-p.slots:
		default:
			return
		}
	}
}

func (p *BoundedBatchPool[T]) observeCapacity() {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationCapacity,
		QueueCapacity: p.cfg.QueueSize,
		Workers:       p.cfg.Workers,
	})
}

func (p *BoundedBatchPool[T]) observeDepth() {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationDepth,
		QueueDepth:    p.QueueDepth(),
		QueueCapacity: p.cfg.QueueSize,
		Workers:       p.cfg.Workers,
	})
}

func (p *BoundedBatchPool[T]) observeAdmission(result string) {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationAdmission,
		Result:        result,
		QueueDepth:    p.QueueDepth(),
		QueueCapacity: p.cfg.QueueSize,
		Workers:       p.cfg.Workers,
	})
}

func (p *BoundedBatchPool[T]) observeWait(wait time.Duration) {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationWait,
		QueueDepth:    p.QueueDepth(),
		QueueCapacity: p.cfg.QueueSize,
		Workers:       p.cfg.Workers,
		Wait:          nonNegativeDuration(wait),
	})
}

func (p *BoundedBatchPool[T]) observeTask(result string, err error, duration time.Duration) {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationTask,
		Result:        result,
		QueueDepth:    p.QueueDepth(),
		QueueCapacity: p.cfg.QueueSize,
		Running:       int(p.running.Load()),
		Workers:       p.cfg.Workers,
		Duration:      nonNegativeDuration(duration),
		Err:           err,
	})
}

func (p *BoundedBatchPool[T]) observeWorker(running int) {
	waiting := 0
	if p.pool != nil {
		waiting = p.pool.Waiting()
	}
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationWorker,
		QueueDepth:    p.QueueDepth(),
		QueueCapacity: p.cfg.QueueSize,
		Running:       running,
		Workers:       p.cfg.Workers,
		Waiting:       waiting,
	})
}

func (p *BoundedBatchPool[T]) observe(obs BoundedPoolObservation) {
	if p == nil || p.cfg.Observer == nil {
		return
	}
	p.cfg.Observer.ObserveBoundedPool(obs)
}
