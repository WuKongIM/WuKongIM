package replication

import (
	"context"
	"errors"
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
	defaultRuntimeReplicaHedge     = 25 * time.Millisecond
	defaultRuntimeTrailingFlush    = 100 * time.Millisecond
	defaultRuntimeRepairWorkers    = 8
	defaultRuntimeRepairRetry      = 10 * time.Millisecond
	defaultRuntimeMaxChannels      = 100_000
	defaultRuntimeMaxVoters        = 3
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
	// ReplicaHedgeDelay bounds how long a foreground quorum waits for its
	// preferred follower before admitting the other voter as a hedge.
	ReplicaHedgeDelay time.Duration
	// TrailingFlushInterval bounds how long a non-quorum follower write waits
	// for cross-channel batching before transport admission.
	TrailingFlushInterval time.Duration
	RepairWorkers         int
	// MaxVoters bounds one installed Channel authority and therefore the
	// runtime-owned follower-repair ledger.
	MaxVoters   int
	QueueItems  int
	QueueBytes  int
	TargetItems int
	TargetBytes int
	BatchItems  int
	BatchBytes  int
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

	localPool *workqueue.BoundedBatchPool[localDurabilityItem]
	peerPool  *workqueue.BoundedPool[func()]
	repairs   *runtimeRepairOwner
	peers     *peerBatcher
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
	timeout      time.Duration
	maxPageBytes int
	retryDelay   time.Duration
	maxPending   int

	mu          sync.Mutex
	pending     map[runtimeRepairKey]*runtimeRepairEntry
	authorities map[ch.ChannelKey]runtimeRepairAuthority
	ready       []runtimeRepairKey
	notify      chan struct{}
	done        chan struct{}
	workers     atomic.Int32
}

type runtimeRepairKey struct {
	channel  ch.ChannelKey
	follower ch.NodeID
}

type runtimeRepairEntry struct {
	repair  followerRepair
	version uint64
	queued  bool
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
}

type runtimeRepairAuthority struct {
	channelID ch.ChannelID
	id        AuthorityID
	voters    []ch.NodeID
}

type followerRepairAuthorityOwner interface {
	InstallAuthority(Authority)
}

// NewRuntime creates the complete bounded durable replication owner for one
// node. It starts no goroutine or timer per Channel.
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	cfg = normalizeRuntimeConfig(cfg)
	if cfg.LocalNode == 0 || cfg.Store == nil || cfg.Link == nil ||
		cfg.LocalWorkers <= 0 || cfg.PeerWorkers <= 0 || cfg.PeerTargetFlight <= 0 || cfg.PeerTargetFlight > cfg.PeerWorkers ||
		cfg.RepairWorkers <= 0 || cfg.MaxVoters <= 0 || cfg.MaxVoters > 256 || cfg.MaxChannels > int(^uint(0)>>1)/cfg.MaxVoters ||
		cfg.QueueItems < cfg.BatchItems || cfg.QueueBytes < cfg.BatchBytes ||
		cfg.TargetItems < cfg.BatchItems || cfg.TargetItems > cfg.QueueItems-cfg.BatchItems ||
		cfg.TargetBytes < cfg.BatchBytes || cfg.TargetBytes > cfg.QueueBytes-cfg.BatchBytes ||
		cfg.BatchItems <= 0 || cfg.BatchItems > MaxExchangeBatchItems || cfg.BatchBytes <= 0 || cfg.BatchBytes > MaxExchangeBatchBytes ||
		cfg.RecoveryPageBytes <= 0 || cfg.RecoveryPageBytes >= cfg.BatchBytes ||
		cfg.ExchangeTimeout <= 0 || cfg.LocalTimeout <= 0 ||
		cfg.ReplicaHedgeDelay <= 0 || cfg.TrailingFlushInterval <= 0 || cfg.RecoveryTimeout <= 0 || cfg.CloseTimeout <= 0 || cfg.MaxChannels <= 0 || cfg.MaxRetainedCommands <= 0 {
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

	repairs, err := newRuntimeRepairOwner(runtimeRepairOwnerConfig{
		Context: ctx, Store: cfg.Store, Peers: peers, Goroutines: cfg.Goroutines,
		Workers: cfg.RepairWorkers, Timeout: cfg.RecoveryTimeout, RetryDelay: defaultRuntimeRepairRetry,
		MaxPageBytes: cfg.RecoveryPageBytes, MaxPending: cfg.MaxChannels * cfg.MaxVoters,
	})
	if err != nil {
		cancel()
		_ = peerPool.Close(context.Background())
		return nil, err
	}
	runtime.repairs = repairs

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
		_ = repairs.Close(context.Background())
		_ = peerPool.Close(context.Background())
		return nil, err
	}
	runtime.localPool = localPool

	dispatcher := &batchingDurabilityDispatcher{
		ownerContext: ctx, local: local, peers: peers, repairs: repairs, hedgeDelay: cfg.ReplicaHedgeDelay,
	}
	recovery := &batchingRecoveryProbeDispatcher{
		local: cfg.LocalNode, ownerContext: ctx, localTimeout: cfg.LocalTimeout,
		store: cfg.Store, peers: peers, executor: executor,
	}
	log, err := newQuorumLog(quorumLogConfig{
		Local: cfg.LocalNode, Store: cfg.Store, Recovery: recovery, Durability: dispatcher,
		RepairAuthorities: repairs,
		RecoveryTimeout:   cfg.RecoveryTimeout, RecoveryPageBytes: cfg.RecoveryPageBytes,
		MaxChannels: cfg.MaxChannels, MaxVoters: cfg.MaxVoters, MaxProposalRecords: cfg.BatchItems,
		MaxProposalBytes: cfg.BatchBytes, MaxRetainedCommands: cfg.MaxRetainedCommands,
	})
	if err != nil {
		cancel()
		_ = localPool.Close(context.Background())
		_ = repairs.Close(context.Background())
		_ = peerPool.Close(context.Background())
		return nil, err
	}
	server, err := NewExchangeServer(ExchangeServerConfig{
		LocalNode: cfg.LocalNode, Store: cfg.Store, Observer: stageObserver, MaxBatchItems: cfg.BatchItems, MaxBatchBytes: cfg.BatchBytes,
	})
	if err != nil {
		cancel()
		_ = localPool.Close(context.Background())
		_ = repairs.Close(context.Background())
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
	if cfg.ReplicaHedgeDelay == 0 {
		cfg.ReplicaHedgeDelay = defaultRuntimeReplicaHedge
	}
	if cfg.TrailingFlushInterval == 0 {
		cfg.TrailingFlushInterval = defaultRuntimeTrailingFlush
	}
	if cfg.RepairWorkers == 0 {
		cfg.RepairWorkers = defaultRuntimeRepairWorkers
	}
	if cfg.MaxVoters == 0 {
		cfg.MaxVoters = defaultRuntimeMaxVoters
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
		if err := r.repairs.Close(ctx); err != nil {
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

type runtimeRepairOwnerConfig struct {
	Context      context.Context
	Store        ReplicaStore
	Peers        *peerBatcher
	Goroutines   *goruntimeregistry.Registry
	Workers      int
	Timeout      time.Duration
	RetryDelay   time.Duration
	MaxPageBytes int
	MaxPending   int
}

func newRuntimeRepairOwner(cfg runtimeRepairOwnerConfig) (*runtimeRepairOwner, error) {
	if cfg.Context == nil || cfg.Store == nil || cfg.Peers == nil || cfg.Workers <= 0 ||
		cfg.Timeout <= 0 || cfg.RetryDelay <= 0 || cfg.MaxPageBytes <= 0 || cfg.MaxPending <= 0 {
		return nil, ch.ErrInvalidConfig
	}
	owner := &runtimeRepairOwner{
		ctx: cfg.Context, store: cfg.Store, peers: cfg.Peers, timeout: cfg.Timeout,
		maxPageBytes: cfg.MaxPageBytes, retryDelay: cfg.RetryDelay, maxPending: cfg.MaxPending,
		pending: make(map[runtimeRepairKey]*runtimeRepairEntry), authorities: make(map[ch.ChannelKey]runtimeRepairAuthority),
		notify: make(chan struct{}, 1), done: make(chan struct{}),
	}
	owner.workers.Store(int32(cfg.Workers))
	goruntimeregistry.SafeGoN(cfg.Goroutines, goruntimeregistry.TaskChannelQuorumOwner, cfg.Workers, func(_ int) {
		defer func() {
			if owner.workers.Add(-1) == 0 {
				close(owner.done)
			}
		}()
		owner.runWorker()
	})
	return owner, nil
}

// RecordFollowerRepair retains exact evidence until a fixed worker proves the
// follower durable. The ledger bound is derived from MaxChannels*MaxVoters;
// reaching it with a new valid key is therefore an internal invariant breach.
func (o *runtimeRepairOwner) RecordFollowerRepair(repair followerRepair) {
	if o == nil || o.ctx.Err() != nil || !validFollowerRepair(repair) {
		return
	}
	key := runtimeRepairKey{channel: repair.channelKey, follower: repair.follower}
	o.mu.Lock()
	authority, known := o.authorities[repair.channelKey]
	if !known || authority.channelID != repair.channelID || authority.id != repairAuthorityID(repair) ||
		!repairAuthorityHasVoter(authority, repair.follower) {
		o.mu.Unlock()
		return
	}
	entry := o.pending[key]
	if entry == nil {
		if len(o.pending) >= o.maxPending {
			o.mu.Unlock()
			panic("channel replication: follower repair ownership bound exceeded")
		}
		entryCtx, cancel := context.WithCancel(o.ctx)
		entry = &runtimeRepairEntry{repair: repair, version: 1, queued: true, ctx: entryCtx, cancel: cancel}
		o.pending[key] = entry
		o.ready = append(o.ready, key)
	} else if merged, changed := mergeFollowerRepair(entry.repair, repair); changed {
		entry.repair = merged
		entry.version++
		if !entry.running && !entry.queued {
			entry.queued = true
			o.ready = append(o.ready, key)
		}
	}
	o.mu.Unlock()
	o.signal()
}

// InstallAuthority replaces the complete voter repair generation for one
// Channel. Older ready and running repairs are canceled before a newer
// authority can publish repair evidence, so membership rotation cannot grow
// the bounded ledger across generations.
func (o *runtimeRepairOwner) InstallAuthority(authority Authority) {
	if o == nil || o.ctx.Err() != nil || !validAuthority(authority) {
		return
	}
	next := runtimeRepairAuthority{
		channelID: authority.ChannelID, id: authority.ID, voters: append([]ch.NodeID(nil), authority.Voters...),
	}
	o.mu.Lock()
	if o.authorities == nil {
		o.authorities = make(map[ch.ChannelKey]runtimeRepairAuthority)
	}
	if current, ok := o.authorities[authority.Key]; ok {
		comparison := compareAuthorityID(authority.ID, current.id)
		if comparison < 0 {
			o.mu.Unlock()
			return
		}
		if comparison == 0 && current.channelID == authority.ChannelID {
			o.mu.Unlock()
			return
		}
	}
	o.authorities[authority.Key] = next
	for key, entry := range o.pending {
		if key.channel != authority.Key {
			continue
		}
		entry.cancel()
		delete(o.pending, key)
	}
	kept := o.ready[:0]
	for _, key := range o.ready {
		if key.channel != authority.Key {
			kept = append(kept, key)
		}
	}
	clear(o.ready[len(kept):])
	o.ready = kept
	o.mu.Unlock()
	o.signal()
}

func repairAuthorityID(repair followerRepair) AuthorityID {
	return AuthorityID{
		ChannelEpoch: repair.manifest.ChannelEpoch,
		LeaderTerm:   repair.manifest.LeaderTerm,
		FenceVersion: repair.manifest.FenceVersion,
	}
}

func repairAuthorityHasVoter(authority runtimeRepairAuthority, voter ch.NodeID) bool {
	for _, candidate := range authority.voters {
		if candidate == voter {
			return true
		}
	}
	return false
}

func validFollowerRepair(repair followerRepair) bool {
	return repair.channelKey != "" && repair.channelID.ID != "" && repair.leader != 0 && repair.follower != 0 &&
		repair.follower != repair.leader && repair.needFrom > 0 && repair.manifest.LastOffset >= repair.needFrom &&
		repair.manifest.ChannelEpoch > 0 && repair.manifest.LeaderTerm > 0 && repair.manifest.FenceVersion > 0
}

func mergeFollowerRepair(current, incoming followerRepair) (followerRepair, bool) {
	comparison := compareRepairAuthority(incoming.manifest, current.manifest)
	if comparison < 0 {
		return current, false
	}
	if comparison > 0 {
		return incoming, true
	}
	merged := current
	if incoming.needFrom < merged.needFrom {
		merged.needFrom = incoming.needFrom
	}
	if incoming.manifest.LastOffset > merged.manifest.LastOffset {
		merged.channelID = incoming.channelID
		merged.leader = incoming.leader
		merged.manifest = incoming.manifest
	}
	return merged, merged != current
}

func compareRepairAuthority(left, right ch.ProposalManifest) int {
	return compareAuthorityID(
		AuthorityID{ChannelEpoch: left.ChannelEpoch, LeaderTerm: left.LeaderTerm, FenceVersion: left.FenceVersion},
		AuthorityID{ChannelEpoch: right.ChannelEpoch, LeaderTerm: right.LeaderTerm, FenceVersion: right.FenceVersion},
	)
}

func (o *runtimeRepairOwner) signal() {
	select {
	case o.notify <- struct{}{}:
	default:
	}
}

func (o *runtimeRepairOwner) runWorker() {
	for {
		key, repair, entry, version, ok := o.take()
		if !ok {
			select {
			case <-o.ctx.Done():
				return
			case <-o.notify:
				continue
			}
		}
		succeeded := o.repair(entry.ctx, repair)
		if !succeeded {
			timer := time.NewTimer(o.retryDelay)
			select {
			case <-o.ctx.Done():
				timer.Stop()
				o.finish(key, entry, version, false)
				return
			case <-timer.C:
			}
		}
		o.finish(key, entry, version, succeeded)
	}
}

func (o *runtimeRepairOwner) take() (runtimeRepairKey, followerRepair, *runtimeRepairEntry, uint64, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for len(o.ready) > 0 {
		key := o.ready[0]
		o.ready[0] = runtimeRepairKey{}
		o.ready = o.ready[1:]
		entry := o.pending[key]
		if entry == nil || !entry.queued || entry.running {
			continue
		}
		entry.queued = false
		entry.running = true
		return key, entry.repair, entry, entry.version, true
	}
	return runtimeRepairKey{}, followerRepair{}, nil, 0, false
}

func (o *runtimeRepairOwner) finish(key runtimeRepairKey, completed *runtimeRepairEntry, version uint64, succeeded bool) {
	o.mu.Lock()
	entry := o.pending[key]
	if entry != nil && entry == completed {
		entry.running = false
		switch {
		case o.ctx.Err() != nil:
			entry.cancel()
			delete(o.pending, key)
		case succeeded && entry.version == version:
			entry.cancel()
			delete(o.pending, key)
		default:
			entry.queued = true
			o.ready = append(o.ready, key)
		}
	}
	o.mu.Unlock()
	o.signal()
}

func (o *runtimeRepairOwner) pendingCount() int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pending)
}

func (o *runtimeRepairOwner) Close(ctx context.Context) error {
	if o == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-o.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *runtimeRepairOwner) repair(parent context.Context, repair followerRepair) bool {
	ctx, cancel := context.WithTimeout(parent, o.timeout)
	defer cancel()
	loaded, ok := o.waitForRepairFrontier(ctx, repair)
	if !ok {
		return false
	}
	return o.repairFromFrontier(ctx, repair, loaded)
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

func (o *runtimeRepairOwner) repairFromFrontier(ctx context.Context, repair followerRepair, loaded LoadResult) bool {
	state := loaded.State
	previous := ch.EntryIdentity{}
	if repair.needFrom > 1 {
		if len(loaded.Entries) != 1 || !loaded.Entries[0].Present {
			return false
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
			return false
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
				return false
			}
			select {
			case <-ctx.Done():
				return false
			case <-errCh:
				return false
			case result := <-resultCh:
				if !result.Status.Durable() {
					return false
				}
			}
			_, entries, ok := ch.SealProposalManifest(proposal.Manifest, proposal.Records)
			if !ok || len(entries) == 0 {
				return false
			}
			previous = entries[len(entries)-1]
			from = proposal.Manifest.LastOffset + 1
		}
	}
	return true
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}
