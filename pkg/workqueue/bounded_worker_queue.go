package workqueue

import (
	"context"
	"errors"
	"sync"
)

// BoundedWorkerQueueHandler processes one item admitted by a BoundedWorkerQueue.
type BoundedWorkerQueueHandler[T any] func(context.Context, T) error

// BoundedWorkerQueueConfig defines direct worker and admission limits.
type BoundedWorkerQueueConfig struct {
	// Name is a stable low-cardinality name for diagnostics.
	Name string
	// Workers is the number of direct worker goroutines.
	Workers int
	// QueueSize bounds accepted work that has not entered a worker.
	QueueSize int
}

// BoundedWorkerQueue admits work into a bounded queue and executes it on direct goroutines.
type BoundedWorkerQueue[T any] struct {
	cfg     BoundedWorkerQueueConfig
	handler BoundedWorkerQueueHandler[T]

	queue chan T
	slots chan struct{}
	stop  chan struct{}

	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex
	// closed gates admission and is protected by mu.
	closed bool

	closeOnce sync.Once
	closeErr  error
	workerWG  sync.WaitGroup
}

// NewBoundedWorkerQueue starts a bounded direct worker queue.
func NewBoundedWorkerQueue[T any](cfg BoundedWorkerQueueConfig, handler BoundedWorkerQueueHandler[T]) (*BoundedWorkerQueue[T], error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || handler == nil {
		return nil, ErrInvalidConfig
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &BoundedWorkerQueue[T]{
		cfg:     cfg,
		handler: handler,
		queue:   make(chan T, cfg.QueueSize),
		slots:   make(chan struct{}, cfg.QueueSize),
		stop:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	for i := 0; i < cfg.QueueSize; i++ {
		q.slots <- struct{}{}
	}
	q.workerWG.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go q.runWorker()
	}
	return q, nil
}

// Submit attempts non-blocking bounded admission.
func (q *BoundedWorkerQueue[T]) Submit(ctx context.Context, item T) error {
	return q.submit(ctx, item, false)
}

// SubmitWait waits for bounded admission until ctx is canceled or capacity is available.
func (q *BoundedWorkerQueue[T]) SubmitWait(ctx context.Context, item T) error {
	return q.submit(ctx, item, true)
}

func (q *BoundedWorkerQueue[T]) submit(ctx context.Context, item T, wait bool) error {
	if q == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return ErrClosed
		}
		select {
		case <-q.slots:
			if err := q.enqueueWithSlotLocked(item); err != nil {
				q.mu.Unlock()
				q.releaseSlot()
				if errors.Is(err, ErrClosed) {
					return err
				}
				continue
			}
			q.mu.Unlock()
			return nil
		default:
		}
		if !wait {
			q.mu.Unlock()
			return ErrFull
		}
		slots := q.slots
		stop := q.stop
		q.mu.Unlock()

		select {
		case <-slots:
			q.mu.Lock()
			if q.closed {
				q.mu.Unlock()
				q.releaseSlot()
				return ErrClosed
			}
			if err := q.enqueueWithSlotLocked(item); err != nil {
				q.mu.Unlock()
				q.releaseSlot()
				if errors.Is(err, ErrClosed) {
					return err
				}
				continue
			}
			q.mu.Unlock()
			return nil
		case <-stop:
			return ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// enqueueWithSlotLocked writes an item after the caller has acquired one free slot.
// The caller must hold q.mu so Close cannot close admission between validation and enqueue.
func (q *BoundedWorkerQueue[T]) enqueueWithSlotLocked(item T) error {
	if q.closed {
		return ErrClosed
	}
	select {
	case q.queue <- item:
		return nil
	default:
		return ErrFull
	}
}

// Close closes admission and drains already accepted work until ctx expires.
func (q *BoundedWorkerQueue[T]) Close(ctx context.Context) error {
	if q == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	q.closeOnce.Do(func() {
		q.mu.Lock()
		q.closed = true
		close(q.stop)
		q.mu.Unlock()

		done := make(chan struct{})
		go func() {
			q.workerWG.Wait()
			close(done)
		}()
		select {
		case <-done:
			q.closeErr = nil
		case <-ctx.Done():
			q.cancel()
			q.closeErr = ctx.Err()
		}
		q.cancel()
	})
	return q.closeErr
}

// QueueDepth returns the accepted-but-not-yet-executing item count.
func (q *BoundedWorkerQueue[T]) QueueDepth() int {
	if q == nil {
		return 0
	}
	return len(q.queue)
}

// Workers returns the configured worker count.
func (q *BoundedWorkerQueue[T]) Workers() int {
	if q == nil {
		return 0
	}
	return q.cfg.Workers
}

// QueueCapacity returns the configured admission capacity.
func (q *BoundedWorkerQueue[T]) QueueCapacity() int {
	if q == nil {
		return 0
	}
	return q.cfg.QueueSize
}

// Closed reports whether admission has been closed.
func (q *BoundedWorkerQueue[T]) Closed() bool {
	if q == nil {
		return true
	}
	q.mu.Lock()
	closed := q.closed
	q.mu.Unlock()
	return closed
}

func (q *BoundedWorkerQueue[T]) runWorker() {
	defer q.workerWG.Done()
	for {
		select {
		case item := <-q.queue:
			q.releaseSlot()
			_ = q.handler(q.ctx, item)
		case <-q.stop:
			q.drain()
			return
		}
	}
}

func (q *BoundedWorkerQueue[T]) drain() {
	for {
		select {
		case item := <-q.queue:
			q.releaseSlot()
			_ = q.handler(q.ctx, item)
		default:
			return
		}
	}
}

func (q *BoundedWorkerQueue[T]) releaseSlot() {
	select {
	case q.slots <- struct{}{}:
	default:
	}
}
