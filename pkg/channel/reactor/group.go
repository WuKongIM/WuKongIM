package reactor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

const (
	defaultStoreAppendWorkerMultiplier  = 2
	defaultStoreApplyWorkerMultiplier   = 2
	defaultStoreWorkerCap               = 128
	defaultMetaResolveWorkers           = 2
	defaultMetaResolveQueueSize         = 64
	defaultColdActivationMinWorkers     = 4
	defaultColdActivationWorkerCap      = 64
	defaultColdActivationQueuePerWorker = 64
	defaultColdActivationMinQueueSize   = 256
	defaultColdActivationQueueCap       = 4096
	defaultReplicationIdlePollInterval  = 100 * time.Millisecond

	// Default worker pool names preserve legacy observer labels for compatibility.
	defaultStoreAppendPoolName    = "channelv2-store-append"
	defaultStoreReadPoolName      = "channelv2-store-read"
	defaultStoreApplyPoolName     = "channelv2-store-apply"
	defaultRPCPoolName            = "channelv2-rpc"
	defaultMetaResolvePoolName    = "channelv2-meta-resolve"
	defaultColdActivationPoolName = "channelv2-cold-activation"
)

// Config wires a group of channel-keyed reactors.
type Config struct {
	// LocalNode is the node id used when applying channel metadata.
	LocalNode ch.NodeID
	// ReactorCount is the number of hash partitions in the group.
	ReactorCount int
	// MailboxSize bounds each priority queue inside every reactor.
	MailboxSize int
	// MaxChannels bounds loaded Channel runtimes on this node. Zero keeps the current unlimited behavior.
	MaxChannels int
	// Store opens channel-scoped stores for reactors and blocking workers.
	Store store.Factory
	// Transport sends channel replication RPCs from blocking workers.
	Transport transport.Client
	// MetaResolver authorizes unloaded cold activation and refreshes loaded runtimes after newer PullHint fences.
	MetaResolver ch.MetaResolver
	// WorkerPools configures bounded pools for blocking store and RPC effects.
	WorkerPools worker.PoolsConfig
	// AppendBatchMaxRecords is the queued record count that triggers a store append flush.
	AppendBatchMaxRecords int
	// AppendBatchMaxBytes is the queued payload byte budget that triggers a store append flush.
	AppendBatchMaxBytes int
	// AppendBatchMaxWait is the maximum age of the oldest queued append before flushing.
	AppendBatchMaxWait time.Duration
	// AppendBatchAdaptiveFlush enables a shorter cold-channel flush delay before the normal batch window.
	AppendBatchAdaptiveFlush bool
	// AppendBatchColdMaxWait is the cold-channel flush delay used when AppendBatchAdaptiveFlush is enabled.
	AppendBatchColdMaxWait time.Duration
	// StoreAppendBatchMaxWait overrides store-append worker cross-channel coalescing wait. Zero keeps the worker default.
	StoreAppendBatchMaxWait time.Duration
	// AppendQueueMaxRequests bounds accepted append requests waiting per channel.
	AppendQueueMaxRequests int
	// AppendQueueMaxBytes bounds accepted append payload bytes waiting per channel.
	AppendQueueMaxBytes int
	// AppendStoreRetryBackoff delays retry after the store append worker pool rejects a batch.
	AppendStoreRetryBackoff time.Duration
	// ReplicationIdlePollInterval delays the next follower poll when a leader has no new records; defaults to 100ms.
	ReplicationIdlePollInterval time.Duration
	// ReplicationMinBackoff is the first retry delay after pull, apply, or ack failures; defaults to 1ms.
	ReplicationMinBackoff time.Duration
	// ReplicationMaxBackoff caps follower replication retry delays after repeated failures; defaults to 100ms.
	ReplicationMaxBackoff time.Duration
	// PullMaxBytes bounds one follower pull response requested from the leader; defaults to 64 KiB.
	PullMaxBytes int
	// LeaderRecentRecordCacheSize bounds recently appended leader log records kept for follower pulls; defaults to 128.
	LeaderRecentRecordCacheSize int
	// LeaderRecentRecordCacheBytes is a retained payload-byte soft cap for the per-channel leader log cache; the newest oversized record may exceed it.
	LeaderRecentRecordCacheBytes int
	// AppendAdmissionGuard can reject local leader appends before reactor admission.
	AppendAdmissionGuard ch.AppendAdmissionGuard
	// IdleSlowdownAfter is the idle duration after the last Append before follower pull intervals begin increasing.
	IdleSlowdownAfter time.Duration
	// IdleEvictAfter is the idle duration after the last Append before a leader may ask caught-up followers to stop.
	IdleEvictAfter time.Duration
	// IdlePullMinInterval is the shortest no-record follower pull delay returned by a leader; defaults to ReplicationIdlePollInterval.
	IdlePullMinInterval time.Duration
	// IdlePullMaxInterval is the longest parked follower pull delay returned by a leader.
	IdlePullMaxInterval time.Duration
	// IdleEvictCheckInterval is the retry interval for lifecycle checks while eviction is blocked.
	IdleEvictCheckInterval time.Duration
	// PullHintRetryInterval is the retry interval for best-effort PullHint while a follower still needs progress.
	PullHintRetryInterval time.Duration
	// FollowerRecoveryProbeInterval is the base delay for parked follower recovery probes. Zero uses the runtime default.
	FollowerRecoveryProbeInterval time.Duration
	// FollowerRecoveryProbeJitter spreads parked follower recovery probes across this bounded window.
	FollowerRecoveryProbeJitter time.Duration
	// Observer receives lightweight reactor and worker metrics; nil uses a no-op observer.
	Observer Observer
}

// Group owns all reactors and routes events by channel key.
type Group struct {
	cfg      Config
	router   Router
	reactors []*Reactor
	pools    *worker.Pools
	// storeCloses owns fallback closes rejected by worker admission across all reactors.
	storeCloses *storeCloseTracker
	nextOp      atomic.Uint64
	closed      atomic.Bool
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
	g := &Group{cfg: cfg, router: router, reactors: make([]*Reactor, cfg.ReactorCount), storeCloses: newStoreCloseTracker()}
	pools, err := worker.NewPools(defaultWorkerPools(cfg), worker.Deps{LocalNode: cfg.LocalNode, Stores: cfg.Store, Transport: cfg.Transport, MetaResolver: cfg.MetaResolver}, g)
	if err != nil {
		return nil, err
	}
	pools.SetQueueObserver(cfg.Observer)
	g.pools = pools
	for i := range g.reactors {
		r := NewReactor(ReactorConfig{
			ID: i, LocalNode: cfg.LocalNode, Store: cfg.Store, Pools: pools, MailboxSize: cfg.MailboxSize,
			MaxChannels:                   reactorChannelBudget(cfg.MaxChannels, cfg.ReactorCount, i),
			MaxChannelsEnabled:            cfg.MaxChannels > 0,
			AppendBatchMaxRecords:         cfg.AppendBatchMaxRecords,
			AppendBatchMaxBytes:           cfg.AppendBatchMaxBytes,
			AppendBatchMaxWait:            cfg.AppendBatchMaxWait,
			AppendBatchAdaptiveFlush:      cfg.AppendBatchAdaptiveFlush,
			AppendBatchColdMaxWait:        cfg.AppendBatchColdMaxWait,
			AppendQueueMaxRequests:        cfg.AppendQueueMaxRequests,
			AppendQueueMaxBytes:           cfg.AppendQueueMaxBytes,
			AppendStoreRetryBackoff:       cfg.AppendStoreRetryBackoff,
			ReplicationIdlePollInterval:   cfg.ReplicationIdlePollInterval,
			ReplicationMinBackoff:         cfg.ReplicationMinBackoff,
			ReplicationMaxBackoff:         cfg.ReplicationMaxBackoff,
			PullMaxBytes:                  cfg.PullMaxBytes,
			LeaderRecentRecordCacheSize:   cfg.LeaderRecentRecordCacheSize,
			LeaderRecentRecordCacheBytes:  cfg.LeaderRecentRecordCacheBytes,
			AppendAdmissionGuard:          cfg.AppendAdmissionGuard,
			IdleSlowdownAfter:             cfg.IdleSlowdownAfter,
			IdleEvictAfter:                cfg.IdleEvictAfter,
			IdlePullMinInterval:           cfg.IdlePullMinInterval,
			IdlePullMaxInterval:           cfg.IdlePullMaxInterval,
			IdleEvictCheckInterval:        cfg.IdleEvictCheckInterval,
			PullHintRetryInterval:         cfg.PullHintRetryInterval,
			FollowerRecoveryProbeInterval: cfg.FollowerRecoveryProbeInterval,
			FollowerRecoveryProbeJitter:   cfg.FollowerRecoveryProbeJitter,
			Observer:                      cfg.Observer,
			NextOpID:                      g.NextOpID,
			storeCloses:                   g.storeCloses,
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
		cfg.ReplicationIdlePollInterval = defaultReplicationIdlePollInterval
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
	if cfg.LeaderRecentRecordCacheSize == 0 {
		cfg.LeaderRecentRecordCacheSize = 128
	}
	if cfg.LeaderRecentRecordCacheSize < 0 {
		cfg.LeaderRecentRecordCacheBytes = 0
	} else if cfg.LeaderRecentRecordCacheBytes <= 0 {
		cfg.LeaderRecentRecordCacheBytes = min(cfg.PullMaxBytes, 256*1024)
	}
	if cfg.IdleSlowdownAfter <= 0 {
		cfg.IdleSlowdownAfter = 30 * time.Second
	}
	if cfg.IdleEvictAfter <= 0 {
		cfg.IdleEvictAfter = 5 * time.Minute
	}
	if cfg.IdlePullMinInterval <= 0 {
		cfg.IdlePullMinInterval = cfg.ReplicationIdlePollInterval
	}
	if cfg.IdlePullMaxInterval <= 0 {
		cfg.IdlePullMaxInterval = 5 * time.Second
	}
	if cfg.IdleEvictCheckInterval <= 0 {
		cfg.IdleEvictCheckInterval = time.Second
	}
	if cfg.PullHintRetryInterval <= 0 {
		cfg.PullHintRetryInterval = time.Second
	}
	if cfg.FollowerRecoveryProbeInterval < 0 {
		cfg.FollowerRecoveryProbeInterval = 0
	}
	if cfg.FollowerRecoveryProbeInterval == 0 {
		cfg.FollowerRecoveryProbeInterval = defaultFollowerRecoveryProbeInterval
	}
	if cfg.FollowerRecoveryProbeJitter < 0 {
		cfg.FollowerRecoveryProbeJitter = 0
	}
	if cfg.FollowerRecoveryProbeJitter == 0 {
		cfg.FollowerRecoveryProbeJitter = defaultFollowerRecoveryProbeJitter
	}
	cfg.Observer = defaultObserver(cfg.Observer)
	return cfg
}

func reactorChannelBudget(total int, reactors int, index int) int {
	if total <= 0 || reactors <= 0 {
		return 0
	}
	base := total / reactors
	rem := total % reactors
	if index < rem {
		return base + 1
	}
	return base
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
	if event.Kind == EventPull && leaderPullObservationEnabled(g.cfg.Observer) {
		every := leaderPullObservationSampleEvery(g.cfg.Observer)
		if every <= 1 || event.OpID == 0 || uint64(event.OpID)%every == 0 {
			event.TickNow = time.Now()
		}
	}
	if err := reactor.Submit(eventPriority(event.Kind), event); err != nil {
		return nil, err
	}
	return future, nil
}

// ReserveAppend fences final leader eviction while a caller verifies loaded state before submitting Append.
func (g *Group) ReserveAppend(key ch.ChannelKey) (func(), error) {
	if g == nil || g.closed.Load() {
		return nil, ch.ErrClosed
	}
	reactor := g.reactors[g.router.PickIndex(key)]
	return reactor.reserveAppend(key), nil
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

// LookupCommittedMessage returns a message only when the owning reactor's HW covers it.
func (g *Group) LookupCommittedMessage(ctx context.Context, id ch.ChannelID, messageID uint64) (ch.Message, bool, error) {
	if g == nil || g.closed.Load() {
		return ch.Message{}, false, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key := ch.ChannelKeyForID(id)
	future, err := g.Submit(ctx, key, Event{Kind: EventLookupCommittedMessage, Key: key, Context: ctx, MessageID: messageID})
	if err != nil {
		return ch.Message{}, false, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.Message{}, false, err
	}
	return result.LookupMessage, result.LookupFound, nil
}

// Complete routes a blocking worker result back to the owning reactor.
func (g *Group) Complete(result worker.Result) {
	delivered := false
	defer func() {
		if !delivered {
			closeWorkerLoadedStore(result)
		}
	}()
	if g == nil {
		return
	}
	key := result.Fence.ChannelKey
	if key == "" || len(g.reactors) == 0 {
		return
	}
	reactor := g.reactors[g.router.PickIndex(key)]
	err := reactor.SubmitCompletion(Event{Kind: EventWorkerResult, Key: key, Worker: result})
	if errors.Is(err, ch.ErrClosed) {
		return
	}
	if err != nil && !errors.Is(err, ch.ErrClosed) {
		panic(err)
	}
	if err == nil {
		delivered = true
		g.cfg.Observer.ObserveWorkerResult(result.Kind, result.Err, result.Duration)
	}
}

func closeWorkerLoadedStore(result worker.Result) {
	if result.StoreLoad != nil && result.StoreLoad.Store != nil {
		_ = result.StoreLoad.Store.Close()
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
	poolErr := g.pools.Close()
	g.storeCloses.sealAndWait()
	return poolErr
}

func eventPriority(kind EventKind) Priority {
	switch kind {
	case EventApplyMeta, EventCancelWaiter, EventWorkerResult, EventPull, EventNotify, EventPullHint, EventClose:
		return PriorityHigh
	case EventTick:
		return PriorityLow
	default:
		return PriorityNormal
	}
}

func defaultWorkerPools(cfg Config) worker.PoolsConfig {
	workers := max(1, cfg.ReactorCount)
	storeAppendWorkers := min(workers*defaultStoreAppendWorkerMultiplier, defaultStoreWorkerCap)
	storeApplyWorkers := min(workers*defaultStoreApplyWorkerMultiplier, defaultStoreWorkerCap)
	queueSize := max(64, cfg.MailboxSize)
	pools := cfg.WorkerPools
	effectiveStoreApplyWorkers := storeApplyWorkers
	if pools.StoreApply.Workers > 0 {
		effectiveStoreApplyWorkers = pools.StoreApply.Workers
	}
	storeCheckpointWorkers := worker.DefaultStoreCheckpointWorkers(effectiveStoreApplyWorkers)
	if cfg.StoreAppendBatchMaxWait > 0 {
		pools.StoreAppend.BatchMaxWait = cfg.StoreAppendBatchMaxWait
	}
	pools.StoreAppend = defaultPoolConfig(pools.StoreAppend, defaultStoreAppendPoolName, storeAppendWorkers, queueSize)
	pools.StoreRead = defaultPoolConfig(pools.StoreRead, defaultStoreReadPoolName, workers, queueSize)
	pools.StoreApply = defaultPoolConfig(pools.StoreApply, defaultStoreApplyPoolName, storeApplyWorkers, queueSize)
	pools.StoreCheckpoint = defaultPoolConfig(pools.StoreCheckpoint, "channelv2-store-checkpoint", storeCheckpointWorkers, queueSize)
	pools.RPC = defaultPoolConfig(pools.RPC, defaultRPCPoolName, workers, queueSize)
	if cfg.MetaResolver != nil {
		pools.MetaResolve = defaultPoolConfig(pools.MetaResolve, defaultMetaResolvePoolName, defaultMetaResolveWorkers, defaultMetaResolveQueueSize)
		coldWorkers := min(max(defaultColdActivationMinWorkers, workers), defaultColdActivationWorkerCap)
		coldQueueSize := min(max(defaultColdActivationMinQueueSize, coldWorkers*defaultColdActivationQueuePerWorker), defaultColdActivationQueueCap)
		pools.ColdActivation = defaultPoolConfig(pools.ColdActivation, defaultColdActivationPoolName, coldWorkers, coldQueueSize)
	}
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
