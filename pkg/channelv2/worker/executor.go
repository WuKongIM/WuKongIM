package worker

import (
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/panjf2000/ants/v2"
)

const workerExecutorStopGrace = 100 * time.Millisecond

var errExecutorOverloaded = errors.New("channelv2 worker executor overloaded")

// executor runs prepared worker task groups on an ants pool.
type executor struct {
	pool *ants.Pool
}

func newExecutor(size int) (*executor, error) {
	if size <= 0 {
		return nil, ch.ErrInvalidConfig
	}
	pool, err := ants.NewPool(
		size,
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(func(any) {}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: create worker executor: %v", ch.ErrInvalidConfig, err)
	}
	return &executor{pool: pool}, nil
}

func (e *executor) submit(fn func()) error {
	if e == nil || e.pool == nil {
		return ch.ErrClosed
	}
	err := e.pool.Submit(fn)
	if err == nil {
		return nil
	}
	if errors.Is(err, ants.ErrPoolOverload) {
		return errExecutorOverloaded
	}
	if errors.Is(err, ants.ErrPoolClosed) {
		return ch.ErrClosed
	}
	return err
}

func (e *executor) running() int {
	if e == nil || e.pool == nil {
		return 0
	}
	return e.pool.Running()
}

func (e *executor) capacity() int {
	if e == nil || e.pool == nil {
		return 0
	}
	return e.pool.Cap()
}

func (e *executor) waiting() int {
	if e == nil || e.pool == nil {
		return 0
	}
	return e.pool.Waiting()
}

func (e *executor) close() error {
	if e == nil || e.pool == nil {
		return nil
	}
	err := e.pool.ReleaseTimeout(workerExecutorStopGrace)
	if errors.Is(err, ants.ErrPoolClosed) {
		return nil
	}
	return err
}
