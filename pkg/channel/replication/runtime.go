package replication

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

const (
	defaultRuntimeBatchItems       = MaxExchangeBatchItems
	defaultRuntimeBatchBytes       = 4 << 20
	defaultRuntimeQueueItems       = 8192
	defaultRuntimeQueueBytes       = 256 << 20
	defaultRuntimeTargetQueueItems = 2048
	defaultRuntimeTargetQueueBytes = 64 << 20
	defaultRuntimeLocalWorkers     = 32
	defaultRuntimePeerWorkers      = 32
	defaultRuntimePeerTargetFlight = 16
	defaultRuntimeTrailingFlush    = 100 * time.Millisecond
	defaultRuntimeRepairWorkers    = 8
	defaultRuntimeRepairShards     = 64
	defaultRuntimeRepairQueue      = 64
	defaultRuntimeMaxChannels      = 100_000
	defaultRuntimeRetainedCommands = 64
)

// RuntimeConfig provides the node-local store and peer exchange seam. Numeric
// limits use production-safe bounded defaults when zero.
type RuntimeConfig struct {
	LocalNode  ch.NodeID
	Store      ReplicaStore
	Link       PeerLink
	Goroutines *goruntimeregistry.Registry
	// Observer receives low-cardinality latency stages for quorum diagnosis.
	Observer StageObserver

	LocalWorkers int
	PeerWorkers  int
	// PeerTargetFlight bounds concurrent follower exchanges for independent
	// Channels to one target. The peer batcher reserves at most one flight for
	// trailing convergence, leaving the remaining flight for quorum traffic.
	PeerTargetFlight int
	// TrailingFlushInterval bounds how long a non-quorum follower write waits
	// for cross-channel batching before transport admission.
	TrailingFlushInterval time.Duration
	RepairWorkers         int
	RepairShards          int
	QueueItems            int
	QueueBytes            int
	TargetItems           int
	TargetBytes           int
	BatchItems            int
	BatchBytes            int
	// RecoveryPageBytes bounds donor payload independently from request envelope bytes.
	RecoveryPageBytes int

	ExchangeTimeout time.Duration
	LocalTimeout    time.Duration
	RecoveryTimeout time.Duration
	CloseTimeout    time.Duration

	MaxChannels         int
	MaxRetainedCommands int
}

// Runtime owns all bounded local durability, peer exchange, recovery, and
// follower-repair execution used by one node's DurableQuorumLog.
type Runtime struct {
	ctx    context.Context
	cancel context.CancelFunc

	log    *quorumLog
	server *ExchangeServer

	localPool  *workqueue.BoundedBatchPool[localDurabilityItem]
	peerPool   *workqueue.BoundedPool[func()]
	repairPool *workqueue.ShardedMailbox[followerRepair]
	peers      *peerBatcher
	// deferredFlushDone joins the one runtime-scoped trailing-follower timer.
	deferredFlushDone chan struct{}

	closeTimeout time.Duration
	closed       atomic.Bool
	closeOnce    sync.Once
	closeErr     error
}

type localDurabilityItem struct {
	proposal    durableProposal
	complete    func(durabilityCompletion)
	submittedAt time.Time
}

type runtimeLocalDurability struct {
	runtime  *Runtime
	store    ReplicaStore
	timeout  time.Duration
	observer StageObserver
}

type runtimePeerExecutor struct {
	pool *workqueue.BoundedPool[func()]
}

func (e runtimePeerExecutor) Submit(task func()) error {
	if e.pool == nil || task == nil {
		return ch.ErrInvalidConfig
	}
	return e.pool.Submit(context.Background(), task)
}

type runtimeRepairOwner struct {
	ctx          context.Context
	store        ReplicaStore
	peers        *peerBatcher
	pool         *workqueue.ShardedMailbox[followerRepair]
	timeout      time.Duration
	maxPageBytes int
}

// NewRuntime creates the complete bounded durable replication owner for one
// node. It starts no goroutine or timer per Channel.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	cfg = normalizeRuntimeConfig(cfg)
	if cfg.LocalNode == 0 || cfg.Store == nil || cfg.Link == nil ||
		cfg.LocalWorkers <= 0 || cfg.PeerWorkers <= 0 || cfg.PeerTargetFlight <= 0 || cfg.PeerTargetFlight > cfg.PeerWorkers ||
		cfg.RepairWorkers <= 0 || cfg.RepairShards <= 0 ||
		cfg.QueueItems < cfg.BatchItems || cfg.QueueBytes < cfg.BatchBytes ||
		cfg.TargetItems < cfg.BatchItems || cfg.TargetItems > cfg.QueueItems-cfg.BatchItems ||
		cfg.TargetBytes < cfg.BatchBytes || cfg.TargetBytes > cfg.QueueBytes-cfg.BatchBytes ||
		cfg.BatchItems <= 0 || cfg.BatchItems > MaxExchangeBatchItems || cfg.BatchBytes <= 0 || cfg.BatchBytes > MaxExchangeBatchBytes ||
		cfg.RecoveryPageBytes <= 0 || cfg.RecoveryPageBytes >= cfg.BatchBytes ||
		cfg.ExchangeTimeout <= 0 || cfg.LocalTimeout <= 0 ||
		cfg.TrailingFlushInterval <= 0 || cfg.RecoveryTimeout <= 0 || cfg.CloseTimeout <= 0 || cfg.MaxChannels <= 0 || cfg.MaxRetainedCommands <= 0 {
		return nil, ch.ErrInvalidConfig
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{ctx: ctx, cancel: cancel, closeTimeout: cfg.CloseTimeout}

	peerPool, err := workqueue.NewBoundedPool(workqueue.BoundedPoolConfig{
		Name: "channel_quorum_peer", Goroutines: cfg.Goroutines, Task: goruntimeregistry.TaskChannelQuorumOwner,
		Workers: cfg.PeerWorkers, QueueSize: cfg.QueueItems, ReleaseTimeout: cfg.CloseTimeout,
	}, func(_ context.Context, task func()) error {
		task()
		return nil
	})
	if err != nil {
		cancel()
		return nil, err
	}
	runtime.peerPool = peerPool
	executor := runtimePeerExecutor{pool: peerPool}
	stageObserver := newSampledStageObserver(cfg.Observer, defaultRuntimeStageSampleEvery)
	peers, err := newPeerBatcher(peerBatcherConfig{
		Link: cfg.Link, Executor: executor, OwnerContext: ctx, ExchangeTimeout: cfg.ExchangeTimeout, Observer: stageObserver,
		MaxTargetFlight: cfg.PeerTargetFlight,
		MaxBatchItems:   cfg.BatchItems,
		MaxBatchBytes:   cfg.BatchBytes,
		MaxQueuedItems:  cfg.QueueItems, MaxQueuedBytes: cfg.QueueBytes,
		MaxTargetQueuedItems: cfg.TargetItems, MaxTargetQueuedBytes: cfg.TargetBytes,
	})
	if err != nil {
		cancel()
		_ = peerPool.Close(context.Background())
		return nil, err
	}

	repairs := &runtimeRepairOwner{
		ctx: ctx, store: cfg.Store, peers: peers, timeout: cfg.RecoveryTimeout, maxPageBytes: cfg.RecoveryPageBytes,
	}
	repairPool, err := workqueue.NewShardedMailbox(workqueue.ShardedMailboxConfig{
		Name: "channel_quorum_repair", Goroutines: cfg.Goroutines, Task: goruntimeregistry.TaskChannelQuorumOwner,
		Shards: cfg.RepairShards, Workers: cfg.RepairWorkers, QueueSizePerShard: defaultRuntimeRepairQueue,
		BatchMaxItems: cfg.BatchItems, ReleaseTimeout: cfg.CloseTimeout,
	}, repairs.handle)
	if err != nil {
		cancel()
		_ = peerPool.Close(context.Background())
		return nil, err
	}
	repairs.pool = repairPool
	runtime.repairPool = repairPool

	local := &runtimeLocalDurability{runtime: runtime, store: cfg.Store, timeout: cfg.LocalTimeout, observer: stageObserver}
	localPool, err := workqueue.NewBoundedBatchPool(workqueue.BoundedBatchPoolConfig[localDurabilityItem]{
		Name: "channel_quorum_local", Goroutines: cfg.Goroutines, Task: goruntimeregistry.TaskChannelQuorumOwner,
		Workers: cfg.LocalWorkers, QueueSize: cfg.QueueItems, ReleaseTimeout: cfg.CloseTimeout,
		Policy: func(localDurabilityItem) workqueue.BatchOptions {
			return workqueue.BatchOptions{MaxItems: cfg.BatchItems, MaxWait: 50 * time.Microsecond}
		},
		CancelAcceptedOnClose: true,
		CancelAccepted: func(item localDurabilityItem, err error) {
			item.complete(durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: err})
		},
		CancelRunningOnClose: true,
	}, local.runBatch)
	if err != nil {
		cancel()
		_ = repairPool.Close(context.Background())
		_ = peerPool.Close(context.Background())
		return nil, err
	}
	runtime.localPool = localPool

	dispatcher := &batchingDurabilityDispatcher{ownerContext: ctx, local: local, peers: peers, repairs: repairs}
	recovery := &batchingRecoveryProbeDispatcher{
		local: cfg.LocalNode, ownerContext: ctx, localTimeout: cfg.LocalTimeout,
		store: cfg.Store, peers: peers, executor: executor,
	}
	log, err := newQuorumLog(quorumLogConfig{
		Local: cfg.LocalNode, Store: cfg.Store, Recovery: recovery, Durability: dispatcher,
		RecoveryTimeout: cfg.RecoveryTimeout, RecoveryPageBytes: cfg.RecoveryPageBytes,
		MaxChannels: cfg.MaxChannels, MaxProposalRecords: cfg.BatchItems,
		MaxProposalBytes: cfg.BatchBytes, MaxRetainedCommands: cfg.MaxRetainedCommands,
	})
	if err != nil {
		cancel()
		_ = localPool.Close(context.Background())
		_ = repairPool.Close(context.Background())
		_ = peerPool.Close(context.Background())
		return nil, err
	}
	server, err := NewExchangeServer(ExchangeServerConfig{
		LocalNode: cfg.LocalNode, Store: cfg.Store, Observer: stageObserver, MaxBatchItems: cfg.BatchItems, MaxBatchBytes: cfg.BatchBytes,
	})
	if err != nil {
		cancel()
		_ = localPool.Close(context.Background())
		_ = repairPool.Close(context.Background())
		_ = peerPool.Close(context.Background())
		return nil, err
	}
	runtime.log = log
	runtime.server = server
	runtime.peers = peers
	runtime.deferredFlushDone = make(chan struct{})
	goruntimeregistry.SafeGo(cfg.Goroutines, goruntimeregistry.TaskChannelQuorumOwner, func() {
		defer close(runtime.deferredFlushDone)
		peers.runDeferredFlusher(cfg.TrailingFlushInterval)
	})
	return runtime, nil
}

func normalizeRuntimeConfig(cfg RuntimeConfig) RuntimeConfig {
	if cfg.Goroutines == nil {
		cfg.Goroutines = goruntimeregistry.Default()
	}
	if cfg.LocalWorkers == 0 {
		cfg.LocalWorkers = defaultRuntimeLocalWorkers
	}
	if cfg.PeerWorkers == 0 {
		cfg.PeerWorkers = defaultRuntimePeerWorkers
	}
	if cfg.PeerTargetFlight == 0 {
		cfg.PeerTargetFlight = defaultRuntimePeerTargetFlight
	}
	if cfg.TrailingFlushInterval == 0 {
		cfg.TrailingFlushInterval = defaultRuntimeTrailingFlush
	}
	if cfg.RepairWorkers == 0 {
		cfg.RepairWorkers = defaultRuntimeRepairWorkers
	}
	if cfg.RepairShards == 0 {
		cfg.RepairShards = defaultRuntimeRepairShards
	}
	if cfg.QueueItems == 0 {
		cfg.QueueItems = defaultRuntimeQueueItems
	}
	if cfg.QueueBytes == 0 {
		cfg.QueueBytes = defaultRuntimeQueueBytes
	}
	if cfg.TargetItems == 0 {
		cfg.TargetItems = defaultRuntimeTargetQueueItems
	}
	if cfg.TargetBytes == 0 {
		cfg.TargetBytes = defaultRuntimeTargetQueueBytes
	}
	if cfg.BatchItems == 0 {
		cfg.BatchItems = defaultRuntimeBatchItems
	}
	if cfg.BatchBytes == 0 {
		cfg.BatchBytes = defaultRuntimeBatchBytes
	}
	if cfg.RecoveryPageBytes == 0 {
		cfg.RecoveryPageBytes = 1 << 20
	}
	if cfg.ExchangeTimeout == 0 {
		cfg.ExchangeTimeout = 5 * time.Second
	}
	if cfg.LocalTimeout == 0 {
		cfg.LocalTimeout = 5 * time.Second
	}
	if cfg.RecoveryTimeout == 0 {
		cfg.RecoveryTimeout = 15 * time.Second
	}
	if cfg.CloseTimeout == 0 {
		cfg.CloseTimeout = 5 * time.Second
	}
	if cfg.MaxChannels == 0 {
		cfg.MaxChannels = defaultRuntimeMaxChannels
	}
	if cfg.MaxRetainedCommands == 0 {
		cfg.MaxRetainedCommands = defaultRuntimeRetainedCommands
	}
	return cfg
}

// Log returns the runtime-owned durable quorum sequencing surface.
func (r *Runtime) Log() DurableQuorumLog {
	if r == nil {
		return nil
	}
	return r.log
}

// ExchangeServer returns the bounded peer endpoint owned by this runtime.
func (r *Runtime) ExchangeServer() *ExchangeServer {
	if r == nil {
		return nil
	}
	return r.server
}

// Close permanently closes admission and joins all accepted work.
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		if r.peers != nil {
			_ = r.peers.flushDeferred()
		}
		r.cancel()
		var errs []error
		if r.deferredFlushDone != nil {
			select {
			case <-r.deferredFlushDone:
			case <-ctx.Done():
				errs = append(errs, ctx.Err())
			}
		}
		if err := r.repairPool.Close(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := r.peerPool.Close(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := r.localPool.Close(ctx); err != nil {
			errs = append(errs, err)
		}
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}

func (l *runtimeLocalDurability) submitLocal(ctx context.Context, proposal durableProposal, complete func(durabilityCompletion)) error {
	if l == nil || l.runtime == nil || l.runtime.localPool == nil || ctx == nil || complete == nil {
		return ch.ErrInvalidConfig
	}
	if l.runtime.closed.Load() {
		return ch.ErrClosed
	}
	return l.runtime.localPool.Submit(ctx, localDurabilityItem{proposal: proposal.freeze(), complete: complete, submittedAt: time.Now()})
}

func (l *runtimeLocalDurability) runBatch(_ context.Context, items []localDurabilityItem) error {
	batchStartedAt := time.Now()
	for _, item := range items {
		observeReplicationStage(l.observer, stageQuorumLocalQueue, nil, batchStartedAt.Sub(item.submittedAt))
	}
	mutations := make([]Mutation, len(items))
	for index, item := range items {
		proposal := item.proposal
		mutations[index] = Mutation{
			ChannelKey: proposal.channelKey, ChannelID: proposal.channelID,
			Manifest: proposal.manifest, Records: proposal.records, Committed: proposal.committed, Class: MutationClassLeaderQuorum,
			ServerAllocatedMessageIDs: proposal.serverAllocatedMessageIDs,
		}
	}
	ctx, cancel := context.WithTimeout(l.runtime.ctx, l.timeout)
	storeStartedAt := time.Now()
	results := l.store.Sync(ctx, mutations)
	cancel()
	var storeErr error
	if len(results) != len(items) {
		storeErr = errInvalidExchangeResult
	} else {
		for index, result := range results {
			if result.Err != nil {
				storeErr = result.Err
				break
			}
			if !validLocalDurabilityResult(items[index].proposal, result) {
				storeErr = errInvalidExchangeResult
				break
			}
		}
	}
	observeReplicationStage(l.observer, stageQuorumLocalStore, storeErr, time.Since(storeStartedAt))
	if len(results) != len(items) {
		for _, item := range items {
			item.complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errInvalidExchangeResult})
		}
		return nil
	}
	for index, item := range items {
		result := results[index]
		completion := durabilityCompletion{outcome: result.Outcome, err: result.Err}
		if !validLocalDurabilityResult(item.proposal, result) {
			completion = durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errInvalidExchangeResult}
		}
		observeReplicationStage(l.observer, stageQuorumLocalEndToEnd, completion.err, time.Since(item.submittedAt))
		item.complete(completion)
	}
	return nil
}

func validLocalDurabilityResult(proposal durableProposal, result MutationResult) bool {
	if !result.Outcome.Valid() {
		return false
	}
	if result.Outcome.Durable() {
		return result.Err == nil && result.LastOffset == proposal.last && result.NeedFrom == 0
	}
	return result.Err != nil && result.LastOffset == 0
}

func (o *runtimeRepairOwner) RecordFollowerRepair(repair followerRepair) {
	if o == nil || o.pool == nil || o.ctx.Err() != nil || repair.channelKey == "" || repair.follower == 0 {
		return
	}
	key := string(repair.channelKey) + ":" + strconv.FormatUint(uint64(repair.follower), 10)
	_ = o.pool.Submit(o.ctx, key, repair)
}

func (o *runtimeRepairOwner) handle(_ context.Context, batch workqueue.MailboxBatch[followerRepair]) error {
	type repairKey struct {
		channel  ch.ChannelKey
		follower ch.NodeID
	}
	coalesced := make(map[repairKey]followerRepair, len(batch.Items))
	for _, repair := range batch.Items {
		key := repairKey{channel: repair.channelKey, follower: repair.follower}
		current, exists := coalesced[key]
		if !exists || repair.needFrom < current.needFrom || repair.manifest.LastOffset > current.manifest.LastOffset {
			if exists && current.needFrom < repair.needFrom {
				repair.needFrom = current.needFrom
			}
			coalesced[key] = repair
		}
	}
	for _, repair := range coalesced {
		o.repair(repair)
	}
	return nil
}

func (o *runtimeRepairOwner) repair(repair followerRepair) {
	ctx, cancel := context.WithTimeout(o.ctx, o.timeout)
	defer cancel()
	loaded, ok := o.waitForRepairFrontier(ctx, repair)
	if !ok {
		return
	}
	o.repairFromFrontier(ctx, repair, loaded)
}

func (o *runtimeRepairOwner) waitForRepairFrontier(ctx context.Context, repair followerRepair) (LoadResult, bool) {
	indexes := []uint64(nil)
	if repair.needFrom > 1 {
		indexes = []uint64{repair.needFrom - 1}
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		loaded, err := o.store.Load(ctx, LoadBatch{Items: []LoadRequest{{
			ChannelKey: repair.channelKey, ChannelID: repair.channelID, ProbeIndexes: indexes,
		}}})
		if err == nil && len(loaded.Items) == 1 && loaded.Items[0].Err == nil &&
			loaded.Items[0].State.LEO >= repair.manifest.LastOffset && loaded.Items[0].State.LEO >= repair.needFrom {
			return loaded.Items[0], true
		}
		select {
		case <-ctx.Done():
			return LoadResult{}, false
		case <-ticker.C:
		}
	}
}

func (o *runtimeRepairOwner) repairFromFrontier(ctx context.Context, repair followerRepair, loaded LoadResult) {
	state := loaded.State
	previous := ch.EntryIdentity{}
	if repair.needFrom > 1 {
		if len(loaded.Entries) != 1 || !loaded.Entries[0].Present {
			return
		}
		previous = loaded.Entries[0].Identity
	}
	from := repair.needFrom
	through := repair.manifest.LastOffset
	for from <= through {
		pageThrough := through
		if pageThrough-from >= maxRecoveryProbeIndexes {
			pageThrough = from + maxRecoveryProbeIndexes - 1
		}
		pages := o.store.Fetch(ctx, []FetchRange{{
			ChannelKey: repair.channelKey, ChannelID: repair.channelID, Expected: state,
			From: from, Through: pageThrough, Previous: previous, MaxBytes: o.maxPageBytes,
		}})
		if len(pages) != 1 || pages[0].Err != nil || len(pages[0].Proposals) == 0 {
			return
		}
		for _, proposal := range pages[0].Proposals {
			request := ReplicateRequest{
				ChannelKey: repair.channelKey, ChannelID: repair.channelID,
				Leader: repair.leader, Follower: repair.follower,
				Manifest: proposal.Manifest, Records: proposal.Records,
				Committed: minUint64(state.Committed, proposal.Manifest.LastOffset),
			}
			resultCh := make(chan ReplicateResult, 1)
			errCh := make(chan error, 1)
			if err := o.peers.submit(ctx, repair.follower, request, func(result ReplicateResult, err error) {
				if err != nil {
					errCh <- err
					return
				}
				resultCh <- result
			}); err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-errCh:
				return
			case result := <-resultCh:
				if !result.Status.Durable() {
					return
				}
			}
			_, entries, ok := ch.SealProposalManifest(proposal.Manifest, proposal.Records)
			if !ok || len(entries) == 0 {
				return
			}
			previous = entries[len(entries)-1]
			from = proposal.Manifest.LastOffset + 1
		}
	}
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}
