package rpc

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/panjf2000/ants/v2"
)

const serviceExecutorStopGrace = 100 * time.Millisecond

// ExecutorStats captures direct ants/v2 pool occupancy.
type ExecutorStats struct {
	// Running is the current number of executing workers.
	Running int
	// Capacity is the configured pool capacity.
	Capacity int
	// Waiting is the current number of blocked submitters.
	Waiting int
}

// Executor runs service tasks on a bounded ants worker pool.
type Executor struct {
	// mu protects pool tuning and capacity bookkeeping.
	mu sync.Mutex
	// pool owns the ants worker pool used to execute service tasks.
	pool *ants.PoolWithFuncGeneric[*serviceTask]
	// capacity records the current configured executor capacity.
	capacity int
	// stopOnce makes Stop release the ants pool at most once.
	stopOnce sync.Once
	// stopErr stores the first pool release result for deterministic repeated Stop calls.
	stopErr error
}

// NewExecutor creates a bounded service task executor.
func NewExecutor(capacity int, observer core.Observer) (*Executor, error) {
	if capacity <= 0 {
		capacity = 1
	}

	pool, err := ants.NewPoolWithFuncGeneric[*serviceTask](
		capacity,
		func(task *serviceTask) {
			if task == nil {
				return
			}
			task.run()
		},
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(func(any) {
			if observer != nil {
				observer.ObserveTransport(core.Event{Name: "service_executor_pool", Result: "panic"})
			}
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: create service executor: %v", core.ErrInvalidConfig, err)
	}

	return &Executor{pool: pool, capacity: capacity}, nil
}

// Tune increases the executor capacity when the pool is still open.
func (e *Executor) Tune(capacity int) {
	if e == nil || capacity <= 0 {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if capacity <= e.capacity || e.pool == nil || e.pool.IsClosed() {
		return
	}
	e.pool.Tune(capacity)
	e.capacity = capacity
}

// Stats returns current direct ants/v2 pool occupancy.
func (e *Executor) Stats() ExecutorStats {
	if e == nil {
		return ExecutorStats{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	stats := ExecutorStats{Capacity: e.capacity}
	if e.pool == nil {
		return stats
	}
	stats.Running = e.pool.Running()
	stats.Capacity = e.pool.Cap()
	stats.Waiting = e.pool.Waiting()
	return stats
}

// Submit schedules task execution on the service executor.
func (e *Executor) Submit(task *serviceTask) error {
	if e == nil || e.pool == nil {
		return core.ErrStopped
	}

	err := e.pool.Invoke(task)
	if err == nil {
		return nil
	}
	if errors.Is(err, ants.ErrPoolOverload) {
		return core.ErrBusy
	}
	if errors.Is(err, ants.ErrPoolClosed) {
		return core.ErrStopped
	}
	return err
}

// Stop releases the executor pool and waits briefly for running tasks.
func (e *Executor) Stop() error {
	if e == nil || e.pool == nil {
		return nil
	}

	e.stopOnce.Do(func() {
		e.stopErr = e.pool.ReleaseTimeout(serviceExecutorStopGrace)
		if errors.Is(e.stopErr, ants.ErrPoolClosed) {
			e.stopErr = nil
		}
	})
	return e.stopErr
}
