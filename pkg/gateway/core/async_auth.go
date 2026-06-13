package core

import (
	"sync"
	"sync/atomic"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/panjf2000/ants/v2"
)

// authExecutor admits and executes bounded async CONNECT authentication work.
type authExecutor struct {
	// server owns session and gateway state needed by auth tasks.
	server *Server
	// workers is the normalized auth worker count.
	workers int
	// capacity is the normalized maximum admitted auth backlog.
	capacity int
	// queued tracks admitted auth tasks awaiting execution.
	queued atomic.Int64
	// closed prevents new auth admission after shutdown.
	closed atomic.Bool
	// tasks holds admitted auth tasks before they are submitted to ants.
	tasks chan asyncAuthTask
	// wake nudges the dispatcher after admission, worker drain, or shutdown.
	wake chan struct{}
	// done is closed when the dispatcher exits.
	done chan struct{}
	// pool runs auth tasks on bounded ants workers.
	pool *ants.PoolWithFuncGeneric[asyncAuthTask]
	// releaseTimeout bounds graceful ants pool release.
	releaseTimeout time.Duration
	// stopOnce makes executor shutdown idempotent.
	stopOnce sync.Once
	// mu protects closing tasks against concurrent submit sends.
	mu sync.RWMutex
	// panicC records worker panics for package tests and diagnostics.
	panicC chan any
}

func newAuthExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*authExecutor, error) {
	opts = gatewaytypes.NormalizeRuntimeOptions(opts)
	e := &authExecutor{
		server:         s,
		workers:        opts.AsyncAuthWorkers,
		capacity:       opts.AsyncAuthQueueCapacity,
		tasks:          make(chan asyncAuthTask, opts.AsyncAuthQueueCapacity),
		wake:           make(chan struct{}, 1),
		done:           make(chan struct{}),
		releaseTimeout: opts.AsyncPoolReleaseTimeout,
		panicC:         make(chan any, 1),
	}

	pool, err := ants.NewPoolWithFuncGeneric[asyncAuthTask](
		opts.AsyncAuthWorkers,
		e.handle,
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(func(v any) {
			select {
			case e.panicC <- v:
			default:
			}
		}),
	)
	if err != nil {
		close(e.done)
		return nil, err
	}
	e.pool = pool
	if s == nil {
		close(e.done)
		return e, nil
	}
	go e.run()
	return e, nil
}

func (e *authExecutor) submit(task asyncAuthTask) bool {
	if e == nil || task.state == nil || task.connect == nil || e.closed.Load() {
		return false
	}
	if !e.reserve() {
		return false
	}

	task.connect = cloneAuthConnectPacket(task.connect)
	task.enqueuedAt = time.Now()

	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed.Load() {
		e.consume(1)
		return false
	}
	select {
	case e.tasks <- task:
		e.notify()
		return true
	default:
		e.consume(1)
		return false
	}
}

func (e *authExecutor) stop() {
	if e == nil {
		return
	}
	e.stopOnce.Do(func() {
		e.mu.Lock()
		e.closed.Store(true)
		close(e.tasks)
		e.notify()
		e.mu.Unlock()

		<-e.done
		if e.pool != nil {
			_ = e.pool.ReleaseTimeout(e.releaseTimeout)
		}
	})
}

func (e *authExecutor) depth() int {
	if e == nil {
		return 0
	}
	return int(e.queued.Load())
}

func (e *authExecutor) totalCapacity() int {
	if e == nil {
		return 0
	}
	return e.capacity
}

func (e *authExecutor) workerCount() int {
	if e == nil {
		return 0
	}
	return e.workers
}

func (e *authExecutor) reserve() bool {
	for {
		queued := e.queued.Load()
		if queued < 0 || queued >= int64(e.capacity) {
			return false
		}
		if e.queued.CompareAndSwap(queued, queued+1) {
			return true
		}
	}
}

func (e *authExecutor) consume(count int) {
	if e == nil || count <= 0 {
		return
	}
	remaining := e.queued.Add(-int64(count))
	if remaining >= 0 {
		return
	}
	e.queued.Add(-remaining)
}

func (e *authExecutor) run() {
	defer close(e.done)
	for {
		select {
		case task, ok := <-e.tasks:
			if !ok {
				return
			}
			if !e.invoke(task) {
				return
			}
		case <-e.wake:
		}
	}
}

func (e *authExecutor) invoke(task asyncAuthTask) bool {
	for {
		if e.closed.Load() {
			e.consume(1)
			return false
		}
		err := e.pool.Invoke(task)
		switch {
		case err == nil:
			return true
		case isAntsStopped(err):
			e.consume(1)
			return false
		case isAntsBusy(err):
			e.waitForWorker()
		default:
			e.consume(1)
			return true
		}
	}
}

func (e *authExecutor) waitForWorker() {
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()

	select {
	case <-e.wake:
	case <-timer.C:
	}
}

func (e *authExecutor) handle(task asyncAuthTask) {
	defer e.notify()

	e.consume(1)
	if e.server == nil {
		return
	}
	e.server.observeAsyncAuthQueue(e)
	e.server.observeAsyncAuthWait(task)
	e.server.runAuthTask(task)
}

func (e *authExecutor) notify() {
	if e == nil {
		return
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
