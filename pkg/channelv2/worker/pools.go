package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// Pools owns all blocking worker pools used by channelv2 reactors.
type Pools struct {
	StoreAppend *Pool
	StoreRead   *Pool
	StoreApply  *Pool
	RPC         *Pool
}

// PoolsConfig defines worker and queue limits for each blocking class.
type PoolsConfig struct {
	StoreAppend PoolConfig
	StoreRead   PoolConfig
	StoreApply  PoolConfig
	RPC         PoolConfig
}

// NewPools starts all bounded worker pools used by reactors.
func NewPools(cfg PoolsConfig, deps Deps, sink CompletionSink) (*Pools, error) {
	if sink == nil {
		return nil, ch.ErrInvalidConfig
	}
	pools := &Pools{}
	var err error
	if pools.StoreAppend, err = NewPool(cfg.StoreAppend, deps, sink); err != nil {
		return nil, err
	}
	if pools.StoreRead, err = NewPool(cfg.StoreRead, deps, sink); err != nil {
		_ = pools.Close()
		return nil, err
	}
	if pools.StoreApply, err = NewPool(cfg.StoreApply, deps, sink); err != nil {
		_ = pools.Close()
		return nil, err
	}
	if pools.RPC, err = NewPool(cfg.RPC, deps, sink); err != nil {
		_ = pools.Close()
		return nil, err
	}
	return pools, nil
}

// SetQueueObserver installs a queue depth observer on every pool.
func (p *Pools) SetQueueObserver(observer QueueObserver) {
	if p == nil {
		return
	}
	for _, pool := range []*Pool{p.StoreAppend, p.StoreRead, p.StoreApply, p.RPC} {
		pool.SetQueueObserver(observer)
	}
}

// Submit routes a task to the pool assigned to its blocking category.
func (p *Pools) Submit(ctx context.Context, task Task) error {
	pool := p.poolFor(task.Kind)
	if pool == nil {
		return ch.ErrInvalidConfig
	}
	return pool.Submit(ctx, task)
}

// Close stops every pool in the group.
func (p *Pools) Close() error {
	if p == nil {
		return nil
	}
	var first error
	for _, pool := range []*Pool{p.StoreAppend, p.StoreRead, p.StoreApply, p.RPC} {
		if err := pool.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// QueueDepth returns the queued task count for a task kind's pool.
func (p *Pools) QueueDepth(kind TaskKind) int {
	pool := p.poolFor(kind)
	if pool == nil {
		return 0
	}
	return pool.QueueDepth()
}

func (p *Pools) poolFor(kind TaskKind) *Pool {
	if p == nil {
		return nil
	}
	switch kind {
	case TaskStoreAppend:
		return p.StoreAppend
	case TaskStoreReadLog:
		return p.StoreRead
	case TaskStoreApply, TaskStoreCheckpoint:
		return p.StoreApply
	case TaskRPCPull, TaskRPCAck, TaskRPCNotify, TaskRPCPullHint:
		return p.RPC
	default:
		return nil
	}
}
