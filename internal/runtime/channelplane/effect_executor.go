package channelplane

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type effectTask func()

type effectExecutorOptions struct {
	Workers   int
	QueueSize int
}

type effectExecutor struct {
	opts effectExecutorOptions

	mu      sync.Mutex
	started bool
	closed  bool
	queue   chan effectTask
	stopc   chan struct{}
	done    chan struct{}
	wg      sync.WaitGroup
}

func newEffectExecutor(opts effectExecutorOptions) *effectExecutor {
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1
	}
	return &effectExecutor{opts: opts, queue: make(chan effectTask, opts.QueueSize), stopc: make(chan struct{}), done: make(chan struct{})}
}

func (e *effectExecutor) start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started || e.closed {
		return
	}
	e.started = true
	e.wg.Add(e.opts.Workers)
	for i := 0; i < e.opts.Workers; i++ {
		go e.runWorker()
	}
	go func() {
		e.wg.Wait()
		close(e.done)
	}()
}

func (e *effectExecutor) submit(ctx context.Context, task effectTask) error {
	if e == nil || task == nil {
		return channel.ErrInvalidConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.closed || !e.started {
		e.mu.Unlock()
		return ErrClosed
	}
	select {
	case e.queue <- task:
		e.mu.Unlock()
		return nil
	case <-ctx.Done():
		e.mu.Unlock()
		return ctx.Err()
	default:
		e.mu.Unlock()
		return ErrOverloaded
	}
}

func (e *effectExecutor) stop(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	if e.closed {
		done := e.done
		e.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	e.closed = true
	close(e.stopc)
	done := e.done
	e.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *effectExecutor) runWorker() {
	defer e.wg.Done()
	for {
		select {
		case task := <-e.queue:
			if task != nil {
				task()
			}
		case <-e.stopc:
			return
		}
	}
}
