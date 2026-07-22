package channelappend

import (
	"context"
	"runtime"
	"sync/atomic"

	"github.com/panjf2000/ants/v2"
)

// workerPool runs channel append work off the caller stack.
type workerPool struct {
	pool        *ants.Pool
	nonblocking bool
	// logicalInflight reserves non-blocking task capacity until the task body
	// has returned. It excludes idle ants workers, whose Running count is not a
	// usable admission signal when worker purging is disabled.
	logicalInflight atomic.Int64
}

func newWorkerPool(size int) *workerPool {
	return newWorkerPoolWithAdmission(size, false)
}

// newNonblockingWorkerPool creates a bounded pool whose saturated admission
// returns an error instead of pinning the caller until a worker becomes free.
func newNonblockingWorkerPool(size int) *workerPool {
	return newWorkerPoolWithAdmission(size, true)
}

func newWorkerPoolWithAdmission(size int, nonblocking bool) *workerPool {
	if size <= 0 {
		size = 1
	} else if size > maxWorkerPoolSize {
		size = maxWorkerPoolSize
	}
	pool, err := ants.NewPool(size, ants.WithNonblocking(nonblocking), ants.WithDisablePurge(true))
	if err != nil {
		panic("channelappend: create worker pool: " + err.Error())
	}
	return &workerPool{pool: pool, nonblocking: nonblocking}
}

// submit runs fn on a pooled goroutine. It blocks briefly if all workers are busy.
func (p *workerPool) submit(fn func()) error {
	return p.submitWithCompletion(fn, nil)
}

// submitWithCompletion runs onDone after logical non-blocking capacity is
// released. A just-completed ants worker may not yet have returned to its idle
// queue, so the capacity owner retries that tiny handoff window instead of
// reporting a false overload to the durable retry scheduler.
func (p *workerPool) submitWithCompletion(fn func(), onDone func()) error {
	if !p.nonblocking {
		if onDone == nil {
			return p.pool.Submit(fn)
		}
		return p.pool.Submit(func() {
			defer onDone()
			fn()
		})
	}
	if !p.tryAcquireLogicalCapacity() {
		return ants.ErrPoolOverload
	}
	wrapped := func() {
		defer func() {
			remaining := p.logicalInflight.Add(-1)
			if remaining < 0 {
				panic("channelappend: worker pool logical inflight underflow")
			}
			if onDone != nil {
				onDone()
			}
		}()
		fn()
	}
	for {
		err := p.pool.Submit(wrapped)
		if err == nil {
			return nil
		}
		if err == ants.ErrPoolOverload {
			// logical capacity proves a prior task has returned; ants only needs
			// to finish putting that worker back on its idle queue.
			runtime.Gosched()
			continue
		}
		p.logicalInflight.Add(-1)
		return err
	}
}

func (p *workerPool) tryAcquireLogicalCapacity() bool {
	capacity := int64(p.pool.Cap())
	for {
		inflight := p.logicalInflight.Load()
		if inflight >= capacity {
			return false
		}
		if p.logicalInflight.CompareAndSwap(inflight, inflight+1) {
			return true
		}
	}
}

// running reports logical admitted tasks for a non-blocking pool and currently
// executing ants workers for a blocking pool.
func (p *workerPool) running() int {
	if p.nonblocking {
		return int(p.logicalInflight.Load())
	}
	return p.pool.Running()
}

// capacity reports the configured worker capacity.
func (p *workerPool) capacity() int {
	return p.pool.Cap()
}

// waiting reports the number of callers blocked waiting for a worker.
func (p *workerPool) waiting() int {
	return p.pool.Waiting()
}

func (p *workerPool) stop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.pool.Release()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
