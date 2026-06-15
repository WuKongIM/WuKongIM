package workqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
)

const (
	defaultReleaseTimeout = 100 * time.Millisecond
	executorRetryDelay    = 10 * time.Microsecond
)

// BoundedPoolHandler processes one item admitted by a BoundedPool.
type BoundedPoolHandler[T any] func(context.Context, T) error

// BoundedPoolConfig defines worker and admission limits for a bounded pool.
type BoundedPoolConfig struct {
	// Name is a stable low-cardinality name used in observations.
	Name string
	// Workers is the maximum number of concurrently executing handler calls.
	Workers int
	// QueueSize bounds accepted work that has not yet entered the executor.
	QueueSize int
	// ReleaseTimeout bounds graceful ants pool release after Close.
	ReleaseTimeout time.Duration
	// Observer receives low-level pool events. It must be concurrency-safe and non-blocking.
	Observer BoundedPoolObserver
}

// BoundedPool admits work into a bounded queue and executes it on an ants pool.
type BoundedPool[T any] struct {
	cfg     BoundedPoolConfig
	handler BoundedPoolHandler[T]

	queue chan boundedPoolTask[T]
	slots chan struct{}
	stop  chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	pool   *ants.PoolWithFuncGeneric[boundedPoolTask[T]]

	closed  atomic.Bool
	running atomic.Int64

	closeOnce  sync.Once
	closeErr   error
	dispatchWG sync.WaitGroup
	taskWG     sync.WaitGroup
}

type boundedPoolTask[T any] struct {
	item       T
	enqueuedAt time.Time
}

// NewBoundedPool starts a bounded worker pool.
func NewBoundedPool[T any](cfg BoundedPoolConfig, handler BoundedPoolHandler[T]) (*BoundedPool[T], error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || handler == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.ReleaseTimeout <= 0 {
		cfg.ReleaseTimeout = defaultReleaseTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &BoundedPool[T]{
		cfg:     cfg,
		handler: handler,
		queue:   make(chan boundedPoolTask[T], cfg.QueueSize),
		slots:   make(chan struct{}, cfg.QueueSize),
		stop:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	pool, err := ants.NewPoolWithFuncGeneric[boundedPoolTask[T]](
		cfg.Workers,
		p.runTask,
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
	)
	if err != nil {
		cancel()
		return nil, err
	}
	p.pool = pool
	p.observeCapacity()
	p.observeDepth()
	p.dispatchWG.Add(1)
	go p.dispatch()
	return p, nil
}

// Submit attempts non-blocking bounded admission.
func (p *BoundedPool[T]) Submit(ctx context.Context, item T) error {
	return p.submit(ctx, item, false)
}

// SubmitWait waits for bounded admission until ctx is canceled or capacity is available.
func (p *BoundedPool[T]) SubmitWait(ctx context.Context, item T) error {
	return p.submit(ctx, item, true)
}

func (p *BoundedPool[T]) submit(ctx context.Context, item T, wait bool) error {
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
	if err := p.acquireSlot(ctx, wait); err != nil {
		p.observeAdmission(errorResult(err))
		return err
	}
	task := boundedPoolTask[T]{item: item, enqueuedAt: time.Now()}
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
		p.observeAdmission(resultFull)
		p.observeDepth()
		return ErrFull
	}
}

func (p *BoundedPool[T]) acquireSlot(ctx context.Context, wait bool) error {
	if wait {
		select {
		case p.slots <- struct{}{}:
			return nil
		case <-p.stop:
			return ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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

// Close closes admission and drains already accepted work until ctx expires.
func (p *BoundedPool[T]) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		close(p.stop)
		done := make(chan struct{})
		go func() {
			p.dispatchWG.Wait()
			p.taskWG.Wait()
			close(done)
		}()
		select {
		case <-done:
			p.closeErr = nil
		case <-ctx.Done():
			p.cancel()
			p.closeErr = ctx.Err()
		}
		if p.pool != nil {
			_ = p.pool.ReleaseTimeout(p.cfg.ReleaseTimeout)
		}
		p.cancel()
	})
	return p.closeErr
}

// QueueDepth returns the accepted-but-not-yet-executing item count.
func (p *BoundedPool[T]) QueueDepth() int {
	if p == nil {
		return 0
	}
	return len(p.slots)
}

// Workers returns the configured worker capacity.
func (p *BoundedPool[T]) Workers() int {
	if p == nil {
		return 0
	}
	return p.cfg.Workers
}

// QueueCapacity returns the configured admission capacity.
func (p *BoundedPool[T]) QueueCapacity() int {
	if p == nil {
		return 0
	}
	return p.cfg.QueueSize
}

func (p *BoundedPool[T]) dispatch() {
	defer p.dispatchWG.Done()
	for {
		select {
		case task := <-p.queue:
			p.observeDepth()
			if !p.submitToExecutor(task) {
				return
			}
		case <-p.stop:
			p.drainQueue()
			return
		}
	}
}

func (p *BoundedPool[T]) drainQueue() {
	for {
		select {
		case task := <-p.queue:
			if !p.submitToExecutor(task) {
				return
			}
		default:
			p.observeDepth()
			return
		}
	}
}

func (p *BoundedPool[T]) submitToExecutor(task boundedPoolTask[T]) bool {
	for {
		p.taskWG.Add(1)
		err := p.pool.Invoke(task)
		if err == nil {
			p.releaseSlots(1)
			p.observeDepth()
			return true
		}
		p.taskWG.Done()
		if errors.Is(err, ants.ErrPoolClosed) {
			p.releaseSlots(1)
			p.observeDepth()
			return false
		}
		if !errors.Is(err, ants.ErrPoolOverload) {
			p.releaseSlots(1)
			p.observeDepth()
			return true
		}
		timer := time.NewTimer(executorRetryDelay)
		select {
		case <-timer.C:
		case <-p.ctx.Done():
			timer.Stop()
			p.releaseSlots(1)
			p.observeDepth()
			return false
		}
	}
}

func (p *BoundedPool[T]) runTask(task boundedPoolTask[T]) {
	defer p.taskWG.Done()
	running := int(p.running.Add(1))
	p.observeWorker(running)
	defer func() {
		running := int(p.running.Add(-1))
		p.observeWorker(running)
	}()
	p.observeWait(time.Since(task.enqueuedAt))
	started := time.Now()
	var err error
	defer func() {
		if v := recover(); v != nil {
			p.observeTask(resultPanic, nil, time.Since(started))
			panic(v)
		}
		p.observeTask(errorResult(err), err, time.Since(started))
	}()
	err = p.handler(p.ctx, task.item)
}

func (p *BoundedPool[T]) releaseSlots(count int) {
	for i := 0; i < count; i++ {
		select {
		case <-p.slots:
		default:
			return
		}
	}
}

func (p *BoundedPool[T]) observeCapacity() {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationCapacity,
		QueueCapacity: p.cfg.QueueSize,
		Workers:       p.cfg.Workers,
	})
}

func (p *BoundedPool[T]) observeDepth() {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationDepth,
		QueueDepth:    p.QueueDepth(),
		QueueCapacity: p.cfg.QueueSize,
		Workers:       p.cfg.Workers,
	})
}

func (p *BoundedPool[T]) observeAdmission(result string) {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationAdmission,
		Result:        result,
		QueueDepth:    p.QueueDepth(),
		QueueCapacity: p.cfg.QueueSize,
		Workers:       p.cfg.Workers,
	})
}

func (p *BoundedPool[T]) observeWait(wait time.Duration) {
	p.observe(BoundedPoolObservation{
		Name:          p.cfg.Name,
		Kind:          observationWait,
		QueueDepth:    p.QueueDepth(),
		QueueCapacity: p.cfg.QueueSize,
		Workers:       p.cfg.Workers,
		Wait:          nonNegativeDuration(wait),
	})
}

func (p *BoundedPool[T]) observeTask(result string, err error, duration time.Duration) {
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

func (p *BoundedPool[T]) observeWorker(running int) {
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

func (p *BoundedPool[T]) observe(obs BoundedPoolObservation) {
	if p == nil || p.cfg.Observer == nil {
		return
	}
	p.cfg.Observer.ObserveBoundedPool(obs)
}

func contextResult(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return resultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return resultTimeout
	default:
		return resultError
	}
}

func errorResult(err error) string {
	switch {
	case err == nil:
		return resultOK
	case errors.Is(err, ErrFull):
		return resultFull
	case errors.Is(err, ErrClosed):
		return resultClosed
	default:
		return contextResult(err)
	}
}

func nonNegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
