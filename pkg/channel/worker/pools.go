package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

const (
	defaultStoreCheckpointPoolName      = "channelv2-store-checkpoint"
	defaultStoreCheckpointWorkerDivisor = 4
	defaultStoreCheckpointWorkerCap     = 64
)

// DefaultStoreCheckpointWorkers returns the bounded checkpoint worker count derived from store-apply capacity.
func DefaultStoreCheckpointWorkers(storeApplyWorkers int) int {
	return max(1, min(storeApplyWorkers/defaultStoreCheckpointWorkerDivisor, defaultStoreCheckpointWorkerCap))
}

// Pools owns all blocking worker pools used by channel reactors.
type Pools struct {
	StoreAppend *Pool
	StoreRead   *Pool
	StoreApply  *Pool
	// StoreCheckpoint isolates durable HW maintenance from foreground follower apply work.
	StoreCheckpoint *Pool
	RPC             *Pool
	MetaResolve     *Pool
	// ColdActivation isolates unloaded-channel authority resolution and store loading from hot runtime work.
	ColdActivation *Pool
}

// PoolsConfig defines worker and queue limits for each blocking class.
type PoolsConfig struct {
	StoreAppend PoolConfig
	StoreRead   PoolConfig
	StoreApply  PoolConfig
	// StoreCheckpoint bounds durable HW maintenance independently from follower apply work.
	StoreCheckpoint PoolConfig
	RPC             PoolConfig
	MetaResolve     PoolConfig
	// ColdActivation bounds unloaded-channel authority resolution and store loading independently from hot runtime work.
	ColdActivation PoolConfig
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
	checkpointCfg := cfg.StoreCheckpoint
	if checkpointCfg.Name == "" {
		checkpointCfg.Name = defaultStoreCheckpointPoolName
	}
	if checkpointCfg.Workers <= 0 {
		checkpointCfg.Workers = DefaultStoreCheckpointWorkers(cfg.StoreApply.Workers)
	}
	if checkpointCfg.QueueSize <= 0 {
		checkpointCfg.QueueSize = cfg.StoreApply.QueueSize
	}
	if pools.StoreCheckpoint, err = NewPool(checkpointCfg, deps, sink); err != nil {
		_ = pools.Close()
		return nil, err
	}
	if pools.RPC, err = NewPool(cfg.RPC, deps, sink); err != nil {
		_ = pools.Close()
		return nil, err
	}
	if deps.MetaResolver != nil {
		if pools.MetaResolve, err = NewPool(cfg.MetaResolve, deps, sink); err != nil {
			_ = pools.Close()
			return nil, err
		}
		if cfg.ColdActivation.Workers > 0 && cfg.ColdActivation.QueueSize > 0 {
			if pools.ColdActivation, err = NewPool(cfg.ColdActivation, deps, sink); err != nil {
				_ = pools.Close()
				return nil, err
			}
		}
	}
	return pools, nil
}

// SetQueueObserver installs a queue depth observer on every pool.
func (p *Pools) SetQueueObserver(observer QueueObserver) {
	if p == nil {
		return
	}
	for _, pool := range []*Pool{p.StoreAppend, p.StoreRead, p.StoreApply, p.StoreCheckpoint, p.RPC, p.MetaResolve, p.ColdActivation} {
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
	for _, pool := range []*Pool{p.StoreAppend, p.StoreRead, p.StoreApply, p.StoreCheckpoint, p.RPC, p.MetaResolve, p.ColdActivation} {
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
	case TaskStoreLoad, TaskStoreReadLog, TaskStoreLookupMessage, TaskStoreClose:
		return p.StoreRead
	case TaskStoreApply, TaskStoreRetention:
		return p.StoreApply
	case TaskStoreCheckpoint:
		return p.StoreCheckpoint
	case TaskRPCPull, TaskRPCAck, TaskRPCNotify, TaskRPCPullHint:
		return p.RPC
	case TaskMetaResolve:
		return p.MetaResolve
	case TaskColdMetaResolve, TaskColdStoreLoad:
		return p.ColdActivation
	default:
		return nil
	}
}
