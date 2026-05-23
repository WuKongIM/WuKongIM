package worker

import (
	"context"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// PoolConfig defines worker and queue limits for one bounded pool.
type PoolConfig struct {
	Name      string
	Workers   int
	QueueSize int
}

// Pool runs blocking tasks with bounded concurrency and admission.
type Pool struct {
	cfg    PoolConfig
	deps   Deps
	sink   CompletionSink
	queue  chan Task
	stop   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	wg     sync.WaitGroup
}

// NewPool starts a bounded worker pool.
func NewPool(cfg PoolConfig, deps Deps, sink CompletionSink) (*Pool, error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || sink == nil {
		return nil, ch.ErrInvalidConfig
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{cfg: cfg, deps: deps, sink: sink, queue: make(chan Task, cfg.QueueSize), stop: make(chan struct{}), ctx: ctx, cancel: cancel}
	p.wg.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go p.run()
	}
	return p, nil
}

// Submit enqueues task or returns a typed backpressure/closed error.
func (p *Pool) Submit(ctx context.Context, task Task) error {
	if p == nil {
		return ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.stop:
		return ch.ErrClosed
	default:
	}
	select {
	case p.queue <- task:
		return nil
	case <-p.stop:
		return ch.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	default:
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

func (p *Pool) run() {
	defer p.wg.Done()
	for {
		select {
		case task := <-p.queue:
			p.sink.Complete(task.Run(p.ctx, p.deps))
		case <-p.stop:
			return
		}
	}
}
