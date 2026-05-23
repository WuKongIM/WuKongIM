package reactor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

// Config wires a group of channel-keyed reactors.
type Config struct {
	// LocalNode is the node id used when applying channel metadata.
	LocalNode ch.NodeID
	// ReactorCount is the number of hash partitions in the group.
	ReactorCount int
	// MailboxSize bounds each priority queue inside every reactor.
	MailboxSize int
	// Store opens channel-scoped stores for reactors and blocking workers.
	Store store.Factory
	// Transport sends channel replication RPCs from blocking workers.
	Transport transport.Client
	// WorkerPools configures bounded pools for blocking store and RPC effects.
	WorkerPools worker.PoolsConfig
	// AppendBatchMaxRecords is the queued record count that triggers a store append flush.
	AppendBatchMaxRecords int
	// AppendBatchMaxBytes is the queued payload byte budget that triggers a store append flush.
	AppendBatchMaxBytes int
	// AppendBatchMaxWait is the maximum age of the oldest queued append before flushing.
	AppendBatchMaxWait time.Duration
	// AppendQueueMaxRequests bounds accepted append requests waiting per channel.
	AppendQueueMaxRequests int
	// AppendQueueMaxBytes bounds accepted append payload bytes waiting per channel.
	AppendQueueMaxBytes int
	// AppendStoreRetryBackoff delays retry after the store append worker pool rejects a batch.
	AppendStoreRetryBackoff time.Duration
	// ReplicationIdlePollInterval delays the next follower poll when a leader has no new records; defaults to 10ms.
	ReplicationIdlePollInterval time.Duration
	// ReplicationMinBackoff is the first retry delay after pull, apply, or ack failures; defaults to 1ms.
	ReplicationMinBackoff time.Duration
	// ReplicationMaxBackoff caps follower replication retry delays after repeated failures; defaults to 100ms.
	ReplicationMaxBackoff time.Duration
	// PullMaxBytes bounds one follower pull response requested from the leader; defaults to 64 KiB.
	PullMaxBytes int
	// Observer receives lightweight reactor and worker metrics; nil uses a no-op observer.
	Observer Observer
}

// Group owns all reactors and routes events by channel key.
type Group struct {
	cfg      Config
	router   Router
	reactors []*Reactor
	pools    *worker.Pools
	nextOp   atomic.Uint64
	closed   atomic.Bool
}

// NewGroup creates and starts a reactor group.
func NewGroup(cfg Config) (*Group, error) {
	if cfg.LocalNode == 0 || cfg.Store == nil {
		return nil, ch.ErrInvalidConfig
	}
	if cfg.ReactorCount <= 0 {
		cfg.ReactorCount = 1
	}
	if cfg.MailboxSize <= 0 {
		cfg.MailboxSize = 1024
	}
	cfg = defaultConfig(cfg)
	router, err := NewRouter(cfg.ReactorCount)
	if err != nil {
		return nil, err
	}
	g := &Group{cfg: cfg, router: router, reactors: make([]*Reactor, cfg.ReactorCount)}
	pools, err := worker.NewPools(defaultWorkerPools(cfg), worker.Deps{LocalNode: cfg.LocalNode, Stores: cfg.Store, Transport: cfg.Transport}, g)
	if err != nil {
		return nil, err
	}
	pools.SetQueueObserver(cfg.Observer)
	g.pools = pools
	for i := range g.reactors {
		r := NewReactor(ReactorConfig{
			ID: i, LocalNode: cfg.LocalNode, Store: cfg.Store, Pools: pools, MailboxSize: cfg.MailboxSize,
			AppendBatchMaxRecords:       cfg.AppendBatchMaxRecords,
			AppendBatchMaxBytes:         cfg.AppendBatchMaxBytes,
			AppendBatchMaxWait:          cfg.AppendBatchMaxWait,
			AppendQueueMaxRequests:      cfg.AppendQueueMaxRequests,
			AppendQueueMaxBytes:         cfg.AppendQueueMaxBytes,
			AppendStoreRetryBackoff:     cfg.AppendStoreRetryBackoff,
			ReplicationIdlePollInterval: cfg.ReplicationIdlePollInterval,
			ReplicationMinBackoff:       cfg.ReplicationMinBackoff,
			ReplicationMaxBackoff:       cfg.ReplicationMaxBackoff,
			PullMaxBytes:                cfg.PullMaxBytes,
			Observer:                    cfg.Observer,
			NextOpID:                    g.NextOpID,
		})
		g.reactors[i] = r
		r.start()
	}
	return g, nil
}

func defaultConfig(cfg Config) Config {
	if cfg.AppendBatchMaxRecords <= 0 {
		cfg.AppendBatchMaxRecords = 128
	}
	if cfg.AppendBatchMaxBytes <= 0 {
		cfg.AppendBatchMaxBytes = 256 * 1024
	}
	if cfg.AppendBatchMaxWait <= 0 {
		cfg.AppendBatchMaxWait = time.Millisecond
	}
	if cfg.AppendQueueMaxRequests <= 0 {
		cfg.AppendQueueMaxRequests = max(cfg.MailboxSize, 1024)
	}
	if cfg.AppendQueueMaxBytes <= 0 {
		cfg.AppendQueueMaxBytes = 4 * 1024 * 1024
	}
	if cfg.AppendStoreRetryBackoff <= 0 {
		cfg.AppendStoreRetryBackoff = time.Millisecond
	}
	if cfg.ReplicationIdlePollInterval <= 0 {
		cfg.ReplicationIdlePollInterval = 10 * time.Millisecond
	}
	if cfg.ReplicationMinBackoff <= 0 {
		cfg.ReplicationMinBackoff = time.Millisecond
	}
	if cfg.ReplicationMaxBackoff <= 0 {
		cfg.ReplicationMaxBackoff = 100 * time.Millisecond
	}
	if cfg.ReplicationMaxBackoff < cfg.ReplicationMinBackoff {
		cfg.ReplicationMaxBackoff = cfg.ReplicationMinBackoff
	}
	if cfg.PullMaxBytes <= 0 {
		cfg.PullMaxBytes = 64 * 1024
	}
	cfg.Observer = defaultObserver(cfg.Observer)
	return cfg
}

// Submit routes an event to the owning reactor and returns its future.
func (g *Group) Submit(ctx context.Context, key ch.ChannelKey, event Event) (*Future, error) {
	if g == nil || g.closed.Load() {
		return nil, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	future := event.Future
	if future == nil {
		future = NewFuture()
		event.Future = future
	}
	if event.Key == "" {
		event.Key = key
	}
	reactor := g.reactors[g.router.PickIndex(key)]
	if err := reactor.Submit(eventPriority(event.Kind), event); err != nil {
		return nil, err
	}
	return future, nil
}

// HasChannelState reports whether the owning reactor already has runtime state for key.
func (g *Group) HasChannelState(ctx context.Context, key ch.ChannelKey) (bool, error) {
	future, err := g.Submit(ctx, key, Event{Kind: EventCheckState, Key: key})
	if err != nil {
		return false, err
	}
	_, err = future.Await(ctx)
	if errors.Is(err, ch.ErrChannelNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Complete routes a blocking worker result back to the owning reactor.
func (g *Group) Complete(result worker.Result) {
	if g == nil {
		return
	}
	key := result.Fence.ChannelKey
	if key == "" || len(g.reactors) == 0 {
		return
	}
	reactor := g.reactors[g.router.PickIndex(key)]
	err := reactor.SubmitCompletion(Event{Kind: EventWorkerResult, Key: key, Worker: result})
	if err != nil && !errors.Is(err, ch.ErrClosed) {
		panic(err)
	}
	if err == nil {
		g.cfg.Observer.ObserveWorkerResult(result.Kind, result.Err, result.Duration)
	}
}

// Tick asks every reactor to run one low-priority maintenance tick.
func (g *Group) Tick(ctx context.Context) error {
	if g == nil || g.closed.Load() {
		return ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	for _, reactor := range g.reactors {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := reactor.Submit(PriorityLow, Event{Kind: EventTick, TickNow: now})
		if errors.Is(err, ch.ErrBackpressured) {
			continue
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

// NextOpID returns a monotonic operation id for fenced calls.
func (g *Group) NextOpID() ch.OpID {
	return ch.OpID(g.nextOp.Add(1))
}

// Close stops reactors and worker pools owned by the group.
func (g *Group) Close() error {
	if g == nil {
		return nil
	}
	if g.closed.Swap(true) {
		return nil
	}
	var wg sync.WaitGroup
	for _, reactor := range g.reactors {
		wg.Add(1)
		go func(r *Reactor) {
			defer wg.Done()
			r.Close()
		}(reactor)
	}
	wg.Wait()
	return g.pools.Close()
}

func eventPriority(kind EventKind) Priority {
	switch kind {
	case EventApplyMeta, EventCancelWaiter, EventWorkerResult, EventNotify, EventPullHint, EventClose:
		return PriorityHigh
	case EventTick:
		return PriorityLow
	default:
		return PriorityNormal
	}
}

func defaultWorkerPools(cfg Config) worker.PoolsConfig {
	workers := max(1, cfg.ReactorCount)
	queueSize := max(64, cfg.MailboxSize)
	pools := cfg.WorkerPools
	pools.StoreAppend = defaultPoolConfig(pools.StoreAppend, "channelv2-store-append", workers, queueSize)
	pools.StoreRead = defaultPoolConfig(pools.StoreRead, "channelv2-store-read", workers, queueSize)
	pools.StoreApply = defaultPoolConfig(pools.StoreApply, "channelv2-store-apply", workers, queueSize)
	pools.RPC = defaultPoolConfig(pools.RPC, "channelv2-rpc", workers, queueSize)
	return pools
}

func defaultPoolConfig(cfg worker.PoolConfig, name string, workers int, queueSize int) worker.PoolConfig {
	if cfg.Name == "" {
		cfg.Name = name
	}
	if cfg.Workers <= 0 {
		cfg.Workers = workers
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = queueSize
	}
	return cfg
}
