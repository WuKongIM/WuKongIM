package channelappend

import (
	"context"

	"github.com/panjf2000/ants/v2"
)

// workerPool runs channel append work off the caller stack.
type workerPool struct {
	pool *ants.Pool
}

func newWorkerPool(size int) *workerPool {
	if size <= 0 {
		size = 1
	}
	pool, err := ants.NewPool(size, ants.WithNonblocking(false), ants.WithDisablePurge(true))
	if err != nil {
		panic("channelappend: create worker pool: " + err.Error())
	}
	return &workerPool{pool: pool}
}

// submit runs fn on a pooled goroutine. It blocks briefly if all workers are busy.
func (p *workerPool) submit(fn func()) error {
	return p.pool.Submit(fn)
}

// running reports the number of currently executing pool workers.
func (p *workerPool) running() int {
	return p.pool.Running()
}

// capacity reports the configured worker capacity.
func (p *workerPool) capacity() int {
	return p.pool.Cap()
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
