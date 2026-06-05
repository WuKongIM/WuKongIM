package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
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

// queuedTask carries enqueue timing for wait-duration observation.
type queuedTask struct {
	task       Task
	enqueuedAt time.Time
}

type noopQueueObserver struct{}

func (noopQueueObserver) SetWorkerQueueDepth(pool string, depth int) {}

// PoolConfig defines worker and queue limits for one bounded pool.
type PoolConfig struct {
	Name      string
	Workers   int
	QueueSize int
}

// Pool runs blocking tasks with bounded concurrency and admission.
type Pool struct {
	cfg          PoolConfig
	deps         Deps
	sink         CompletionSink
	queue        chan queuedTask
	stop         chan struct{}
	ctx          context.Context
	cancel       context.CancelFunc
	obs          QueueObserver
	inflight     atomic.Int64
	inflightPeak atomic.Int64
	once         sync.Once
	wg           sync.WaitGroup
}

// NewPool starts a bounded worker pool.
func NewPool(cfg PoolConfig, deps Deps, sink CompletionSink) (*Pool, error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || sink == nil {
		return nil, ch.ErrInvalidConfig
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{cfg: cfg, deps: deps, sink: sink, queue: make(chan queuedTask, cfg.QueueSize), stop: make(chan struct{}), ctx: ctx, cancel: cancel, obs: noopQueueObserver{}}
	p.wg.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go p.run()
	}
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
	p.obs = observer
	p.observeQueueCapacity()
	p.observeWorkers()
	p.observeQueueDepth()
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
	queued := queuedTask{task: task, enqueuedAt: time.Now()}
	select {
	case p.queue <- queued:
		return nil
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
}

// Close cancels running tasks and stops workers after they observe shutdown.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.once.Do(func() {
		p.cancel()
		close(p.stop)
	})
	p.wg.Wait()
	return nil
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
	return len(p.queue)
}

func (p *Pool) observeQueueDepth() {
	p.obs.SetWorkerQueueDepth(p.cfg.Name, len(p.queue))
}

func (p *Pool) observeQueueCapacity() {
	obs, ok := p.obs.(QueueCapacityObserver)
	if !ok {
		return
	}
	obs.SetWorkerQueueCapacity(p.cfg.Name, p.cfg.QueueSize)
}

func (p *Pool) observeWorkers() {
	obs, ok := p.obs.(QueueCapacityObserver)
	if !ok {
		return
	}
	obs.SetWorkerWorkers(p.cfg.Name, p.cfg.Workers)
}

func (p *Pool) observeAdmission(result string) {
	obs, ok := p.obs.(AdmissionObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerAdmission(p.cfg.Name, result)
}

func (p *Pool) observeWait(kind TaskKind, d time.Duration) {
	obs, ok := p.obs.(WaitObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerWait(p.cfg.Name, kind, nonNegativeDuration(d))
}

func (p *Pool) observeTask(kind TaskKind, err error, d time.Duration) {
	obs, ok := p.obs.(TaskObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerTask(p.cfg.Name, kind, err, nonNegativeDuration(d))
}

func (p *Pool) observeInflight(inflight int) {
	obs, ok := p.obs.(InflightObserver)
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

func (p *Pool) run() {
	defer p.wg.Done()
	for {
		select {
		case queued := <-p.queue:
			p.observeQueueDepth()
			p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
			running := int(p.inflight.Add(1))
			p.observeInflight(running)
			started := time.Now()
			result := queued.task.Run(p.ctx, p.deps)
			result.Duration = nonNegativeDuration(time.Since(started))
			p.observeTask(queued.task.Kind, result.Err, result.Duration)
			running = int(p.inflight.Add(-1))
			p.observeInflight(running)
			p.sink.Complete(result)
		case <-p.stop:
			return
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
