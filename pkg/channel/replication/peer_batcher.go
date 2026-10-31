package replication

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

var (
	errInvalidExchangeResult = errors.New("channel replication: invalid exchange result")
	errPeerOutcomeUnknown    = errors.New("channel replication: peer outcome unknown")
	errPeerExchangePanic     = errors.New("channel replication: peer exchange panic")
)

// peerExecutor accepts one asynchronous target owner. Submit must not execute
// task inline; a nil result transfers exactly one future invocation.
type peerExecutor interface {
	Submit(task func()) error
}

type peerBatcherConfig struct {
	Link     PeerLink
	Executor peerExecutor
	Observer StageObserver
	// CallbackExecutor accepts terminal tasks independently from network I/O.
	CallbackExecutor peerExecutor
	// CallbackWorkers declares the global bound for callback-owner execution.
	CallbackWorkers int
	// OwnerContext and ExchangeTimeout bound accepted work independently from
	// the caller that submitted it.
	OwnerContext    context.Context
	ExchangeTimeout time.Duration

	MaxBatchItems int
	MaxBatchBytes int
	// MaxTargetFlight bounds concurrent batches to one target. Channel keys remain serialized.
	MaxTargetFlight int
	MaxQueuedItems  int
	MaxQueuedBytes  int
	// Per-target bounds prevent one unavailable follower from consuming the
	// entire global ownership budget.
	MaxTargetQueuedItems int
	MaxTargetQueuedBytes int
}

type peerBatcher struct {
	cfg peerBatcherConfig

	mu         sync.Mutex
	targets    map[ch.NodeID]*peerTargetQueue
	ownedItems int
	ownedBytes int
	nextID     atomic.Uint64
	// callbackReady is a global FIFO of eligible Channel heads. Accepted and
	// executing tasks share callbackOwners until the original callback returns.
	callbackReady  []peerCallbackKey
	callbackOwners int
	stopping       bool
	progress       chan struct{}
}

type peerTargetQueue struct {
	urgent            []queuedPeerItem
	deferred          []queuedPeerItem
	background        []queuedPeerItem
	urgentWorkers     int
	backgroundWorkers int
	inflight          map[ch.ChannelKey]struct{}
	ownedItems        int
	ownedBytes        int
	// callbacks retains each exact terminal envelope through callback return.
	callbacks map[ch.ChannelKey]*peerCallbackQueue
}

type peerCallbackKey struct {
	node    ch.NodeID
	channel ch.ChannelKey
}

type peerCallbackQueue struct {
	items   []*peerCallbackEnvelope
	running bool
	ready   bool
}

// peerCallbackEnvelope owns only one returned item, never its sibling batch.
type peerCallbackEnvelope struct {
	item   queuedPeerItem
	result peerExchangeResult
	err    error
}

type peerWorkClass uint8

const (
	peerWorkUrgent peerWorkClass = iota + 1
	peerWorkBackground
)

func (q *peerTargetQueue) workerCount() int {
	if q == nil {
		return 0
	}
	return q.urgentWorkers + q.backgroundWorkers
}

type queuedPeerItem struct {
	requestID         uint64
	fundingNonce      uint64
	completeFunding   func(StorageFundingResult, error)
	kind              ExchangeKind
	replicate         ReplicateRequest
	probe             ProbeRequest
	fetch             FetchRequest
	bytes             int
	completeReplicate func(ReplicateResult, error)
	completeProbe     func(ProbeResult, error)
	completeFetch     func(FetchResult, error)
	queuedAt          time.Time
}

func (i queuedPeerItem) channelKey() ch.ChannelKey {
	switch i.kind {
	case ExchangeReplicate, ExchangeStoragePrepare, ExchangeStorageCancel:
		return i.replicate.ChannelKey
	case ExchangeProbe:
		return i.probe.ChannelKey
	case ExchangeFetch:
		return i.fetch.ChannelKey
	default:
		return ""
	}
}

type peerExchangeResult struct {
	funding   StorageFundingResult
	replicate ReplicateResult
	probe     ProbeResult
	fetch     FetchResult
}

func newPeerBatcher(cfg peerBatcherConfig) (*peerBatcher, error) {
	if cfg.MaxTargetFlight == 0 {
		cfg.MaxTargetFlight = defaultRuntimePeerTargetFlight
	}
	if cfg.CallbackExecutor == nil {
		cfg.CallbackExecutor = cfg.Executor
	}
	if cfg.CallbackWorkers == 0 {
		cfg.CallbackWorkers = defaultRuntimePeerWorkers
	}
	if cfg.Link == nil || cfg.Executor == nil || cfg.OwnerContext == nil || cfg.OwnerContext.Err() != nil || cfg.ExchangeTimeout <= 0 ||
		cfg.MaxTargetFlight <= 0 || cfg.CallbackExecutor == nil || cfg.CallbackWorkers <= 0 ||
		cfg.MaxBatchItems <= 0 || cfg.MaxBatchBytes <= 0 ||
		cfg.MaxQueuedItems < cfg.MaxBatchItems || cfg.MaxQueuedBytes < cfg.MaxBatchBytes ||
		cfg.MaxTargetQueuedItems < cfg.MaxBatchItems || cfg.MaxTargetQueuedItems > cfg.MaxQueuedItems-cfg.MaxBatchItems ||
		cfg.MaxTargetQueuedBytes < cfg.MaxBatchBytes || cfg.MaxTargetQueuedBytes > cfg.MaxQueuedBytes-cfg.MaxBatchBytes {
		return nil, ch.ErrInvalidConfig
	}
	return &peerBatcher{cfg: cfg, targets: make(map[ch.NodeID]*peerTargetQueue), progress: make(chan struct{}, 1)}, nil
}

// submit admits one immutable follower write. A nil result transfers exactly
// one completion callback to the target owner; caller cancellation after this
// boundary does not revoke durability ownership.
func (b *peerBatcher) submit(ctx context.Context, node ch.NodeID, request ReplicateRequest, complete func(ReplicateResult, error)) error {
	if b == nil || ctx == nil || node == 0 || complete == nil || !request.Valid() || request.Follower != node {
		return ch.ErrInvalidConfig
	}
	return b.enqueue(ctx, node, queuedPeerItem{
		kind: ExchangeReplicate, replicate: request,
		bytes: estimateReplicateRequestBytes(request), completeReplicate: complete,
	}, false)
}

// submitDeferred admits one trailing follower write without immediately
// scheduling transport. The runtime's single bounded flusher promotes all
// accepted trailing writes as one batching wave.
func (b *peerBatcher) submitDeferred(ctx context.Context, node ch.NodeID, request ReplicateRequest, complete func(ReplicateResult, error)) error {
	if b == nil || ctx == nil || node == 0 || complete == nil || !request.Valid() || request.Follower != node {
		return ch.ErrInvalidConfig
	}
	return b.enqueue(ctx, node, queuedPeerItem{
		kind: ExchangeReplicate, replicate: request,
		bytes: estimateReplicateRequestBytes(request), completeReplicate: complete,
	}, true)
}

// submitProbe admits one immutable read-only recovery probe under the same
// per-target and global ownership bounds as data-bearing replication.
func (b *peerBatcher) submitProbe(ctx context.Context, node ch.NodeID, request ProbeRequest, complete func(ProbeResult, error)) error {
	if b == nil || ctx == nil || node == 0 || complete == nil || !request.Valid() || request.Follower != node {
		return ch.ErrInvalidConfig
	}
	request.Indexes = append([]uint64(nil), request.Indexes...)
	return b.enqueue(ctx, node, queuedPeerItem{
		kind: ExchangeProbe, probe: request,
		bytes: estimateProbeRequestBytes(request), completeProbe: complete,
	}, false)
}

// submitFetch admits one immutable recovery donor read under the same bounded
// per-target ownership as replication and probes.
func (b *peerBatcher) submitFetch(ctx context.Context, node ch.NodeID, request FetchRequest, complete func(FetchResult, error)) error {
	if b == nil || ctx == nil || node == 0 || complete == nil || !request.Valid() || request.Follower != node {
		return ch.ErrInvalidConfig
	}
	return b.enqueue(ctx, node, queuedPeerItem{
		kind: ExchangeFetch, fetch: request,
		bytes: estimateFetchRequestBytes(request), completeFetch: complete,
	}, false)
}

func (b *peerBatcher) enqueue(ctx context.Context, node ch.NodeID, item queuedPeerItem, deferred bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.cfg.OwnerContext.Err() != nil {
		return ch.ErrClosed
	}
	if item.bytes > b.cfg.MaxBatchBytes || item.bytes > b.cfg.MaxQueuedBytes {
		return ch.ErrBackpressured
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.cfg.OwnerContext.Err() != nil || b.stopping {
		return ch.ErrClosed
	}
	if b.ownedItems >= b.cfg.MaxQueuedItems || b.ownedBytes+item.bytes > b.cfg.MaxQueuedBytes {
		return ch.ErrBackpressured
	}
	target := b.targets[node]
	if target != nil && (target.ownedItems >= b.cfg.MaxTargetQueuedItems || target.ownedBytes+item.bytes > b.cfg.MaxTargetQueuedBytes) {
		return ch.ErrBackpressured
	}
	if target == nil {
		if item.bytes > b.cfg.MaxTargetQueuedBytes {
			return ch.ErrBackpressured
		}
		target = &peerTargetQueue{}
		b.targets[node] = target
	}
	item.requestID = b.nextID.Add(1)
	item.queuedAt = time.Now()
	if deferred {
		target.deferred = append(target.deferred, item)
	} else {
		target.urgent = append(target.urgent, item)
	}
	target.ownedItems++
	target.ownedBytes += item.bytes
	b.ownedItems++
	b.ownedBytes += item.bytes
	if deferred {
		return nil
	}
	if err := b.ensureTargetWorkersLocked(node, target); err != nil {
		if target.urgentWorkers > 0 {
			return nil
		}
		last := len(target.urgent) - 1
		target.urgent[last] = queuedPeerItem{}
		target.urgent = target.urgent[:last]
		target.ownedItems--
		target.ownedBytes -= item.bytes
		b.ownedItems--
		b.ownedBytes -= item.bytes
		if target.ownedItems == 0 {
			delete(b.targets, node)
		}
		return err
	}
	return nil
}

// flushDeferred promotes every accepted trailing write and schedules bounded
// per-target owners. A scheduling failure leaves promoted work owned so a
// later flush can retry without losing callbacks.
func (b *peerBatcher) flushDeferred() error {
	if b == nil {
		return ch.ErrInvalidConfig
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cfg.OwnerContext.Err() != nil {
		return ch.ErrClosed
	}
	return b.progressPendingLocked()
}

// progressPendingLocked retries only already-accepted work. The cleanup owner
// may use it after I/O cancellation while both executors are still open.
func (b *peerBatcher) progressPendingLocked() error {
	var flushErr error
	for node, target := range b.targets {
		if len(target.deferred) > 0 {
			target.background = append(target.background, target.deferred...)
			clear(target.deferred)
			target.deferred = nil
		}
		if err := b.ensureTargetWorkersLocked(node, target); err != nil {
			flushErr = errors.Join(flushErr, err)
		}
	}
	if err := b.scheduleCallbacksLocked(); err != nil {
		flushErr = errors.Join(flushErr, err)
	}
	return flushErr
}

func (b *peerBatcher) stopAdmission() {
	b.mu.Lock()
	b.stopping = true
	b.mu.Unlock()
}

// drainAccepted keeps the original cleanup owner until every accepted I/O and
// terminal task has returned. Executor refusal retains debt for the next retry.
func (b *peerBatcher) drainAccepted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.progressPendingLocked()
	if b.ownedItems != 0 || b.ownedBytes != 0 || b.callbackOwners != 0 {
		return false
	}
	for _, target := range b.targets {
		if target.workerCount() != 0 || len(target.inflight) != 0 {
			return false
		}
	}
	return true
}

func (b *peerBatcher) signalProgress() {
	select {
	case b.progress <- struct{}{}:
	default:
	}
}

func (b *peerBatcher) runDeferredFlusher(interval time.Duration) {
	if b == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-b.cfg.OwnerContext.Done():
			return
		case <-ticker.C:
			_ = b.flushDeferred()
		}
	}
}

func (b *peerBatcher) scheduleTargetLocked(node ch.NodeID, target *peerTargetQueue, class peerWorkClass) error {
	if target == nil || target.workerCount() >= b.cfg.MaxTargetFlight {
		return nil
	}
	if err := b.cfg.Executor.Submit(func() { b.drainTarget(node, class) }); err != nil {
		return err
	}
	switch class {
	case peerWorkUrgent:
		target.urgentWorkers++
	case peerWorkBackground:
		target.backgroundWorkers++
	}
	return nil
}

// ensureTargetWorkersLocked reserves one target flight for flushed trailing
// replicas while keeping every remaining flight available to quorum, probe,
// and recovery traffic. It must be called with b.mu held.
func (b *peerBatcher) ensureTargetWorkersLocked(node ch.NodeID, target *peerTargetQueue) error {
	if target == nil {
		return nil
	}
	var scheduleErr error
	urgentWasScheduled := target.urgentWorkers > 0
	urgentReady := peerQueueHasReadyItem(target.urgent, target.inflight)
	backgroundReady := peerQueueHasReadyItem(target.background, target.inflight)
	if urgentReady && target.urgentWorkers == 0 && target.workerCount() < b.cfg.MaxTargetFlight {
		if err := b.scheduleTargetLocked(node, target, peerWorkUrgent); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
		}
	}
	if b.cfg.MaxTargetFlight > 1 && backgroundReady && target.backgroundWorkers == 0 && target.workerCount() < b.cfg.MaxTargetFlight {
		if err := b.scheduleTargetLocked(node, target, peerWorkBackground); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
		}
	}
	if b.cfg.MaxTargetFlight == 1 && len(target.urgent) == 0 && backgroundReady && target.backgroundWorkers == 0 && target.workerCount() == 0 {
		if err := b.scheduleTargetLocked(node, target, peerWorkBackground); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
		}
	}
	urgentLimit := b.cfg.MaxTargetFlight
	if target.backgroundWorkers > 0 || len(target.background) > 0 {
		urgentLimit--
	}
	if urgentWasScheduled && urgentReady && len(target.inflight) > 0 &&
		target.urgentWorkers < urgentLimit && target.workerCount() < b.cfg.MaxTargetFlight {
		if err := b.scheduleTargetLocked(node, target, peerWorkUrgent); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
		}
	}
	return scheduleErr
}

func (b *peerBatcher) drainTarget(node ch.NodeID, class peerWorkClass) {
	defer b.finishTargetWorker(node, class)
	for {
		items := b.takeBatch(node, class)
		if len(items) == 0 {
			return
		}
		queueStage, exchangeStage, endToEndStage := peerStageNames(class)
		exchangeStarted := time.Now()
		for index := range items {
			observeReplicationStage(b.cfg.Observer, queueStage, nil, exchangeStarted.Sub(items[index].queuedAt))
		}
		results, errs := b.exchange(node, class, items)
		exchangeFinished := time.Now()
		var exchangeErr error
		for index := range errs {
			if errs[index] != nil {
				exchangeErr = errs[index]
				break
			}
		}
		observeReplicationStage(b.cfg.Observer, exchangeStage, exchangeErr, exchangeFinished.Sub(exchangeStarted))
		for index := range items {
			observeReplicationStage(b.cfg.Observer, endToEndStage, errs[index], exchangeFinished.Sub(items[index].queuedAt))
		}
		b.publishCallbacks(node, items, results, errs)
	}
}

func (b *peerBatcher) finishTargetWorker(node ch.NodeID, class peerWorkClass) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.targets[node]
	if target == nil {
		return
	}
	switch class {
	case peerWorkUrgent:
		if target.urgentWorkers > 0 {
			target.urgentWorkers--
		}
	case peerWorkBackground:
		if target.backgroundWorkers > 0 {
			target.backgroundWorkers--
		}
	}
	_ = b.ensureTargetWorkersLocked(node, target)
	if target.workerCount() == 0 && target.ownedItems == 0 {
		delete(b.targets, node)
	}
	b.signalProgress()
}

// peerQueueHasReadyItem mirrors takeBatch's first-item kind and per-Channel
// ordering barriers. Callers hold b.mu, so in-flight ownership cannot change
// during the scan. A same-kind queue blocked only by in-flight work allocates
// nothing and must await release instead of continuously rescheduling owners.
func peerQueueHasReadyItem(queue []queuedPeerItem, inflight map[ch.ChannelKey]struct{}) bool {
	if len(queue) == 0 {
		return false
	}
	kind := queue[0].kind
	var blocked map[ch.ChannelKey]struct{}
	for _, item := range queue {
		key := item.channelKey()
		if _, busy := inflight[key]; busy {
			continue
		}
		if item.kind != kind {
			if blocked == nil {
				blocked = make(map[ch.ChannelKey]struct{})
			}
			blocked[key] = struct{}{}
			continue
		}
		if _, earlierKind := blocked[key]; !earlierKind {
			return true
		}
	}
	return false
}

func (b *peerBatcher) takeBatch(node ch.NodeID, class peerWorkClass) []queuedPeerItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.targets[node]
	if target == nil {
		return nil
	}
	queue := target.urgent
	if class == peerWorkBackground {
		queue = target.background
	}
	if !peerQueueHasReadyItem(queue, target.inflight) {
		return nil
	}
	kind := queue[0].kind
	items := make([]queuedPeerItem, 0, min(len(queue), b.cfg.MaxBatchItems))
	remaining := queue[:0]
	batchBytes := 0
	var blockedChannels map[ch.ChannelKey]struct{}
	for index := range queue {
		item := queue[index]
		channelKey := item.channelKey()
		if _, busy := target.inflight[channelKey]; busy {
			remaining = append(remaining, item)
			continue
		}
		if len(items) >= b.cfg.MaxBatchItems {
			remaining = append(remaining, item)
			continue
		}
		if item.kind != kind {
			if blockedChannels == nil {
				blockedChannels = make(map[ch.ChannelKey]struct{})
			}
			blockedChannels[channelKey] = struct{}{}
			remaining = append(remaining, item)
			continue
		}
		if _, blocked := blockedChannels[channelKey]; blocked {
			remaining = append(remaining, item)
			continue
		}
		if len(items) > 0 && batchBytes+item.bytes > b.cfg.MaxBatchBytes {
			if blockedChannels == nil {
				blockedChannels = make(map[ch.ChannelKey]struct{})
			}
			blockedChannels[channelKey] = struct{}{}
			remaining = append(remaining, item)
			continue
		}
		batchBytes += item.bytes
		items = append(items, item)
		if target.inflight == nil {
			target.inflight = make(map[ch.ChannelKey]struct{})
		}
		target.inflight[channelKey] = struct{}{}
	}
	if class == peerWorkBackground {
		clear(queue[len(remaining):])
		target.background = remaining
	} else {
		clear(queue[len(remaining):])
		target.urgent = remaining
	}
	return items
}

func (b *peerBatcher) exchange(node ch.NodeID, class peerWorkClass, items []queuedPeerItem) ([]peerExchangeResult, []error) {
	results := make([]peerExchangeResult, len(items))
	errs := make([]error, len(items))
	priority := ExchangePriorityForeground
	if class == peerWorkBackground {
		priority = ExchangePriorityBackground
	}
	batch := ExchangeBatch{Version: ExchangeVersion, Priority: priority, Items: make([]ExchangeItem, len(items))}
	for index, item := range items {
		switch item.kind {
		case ExchangeReplicate, ExchangeStoragePrepare, ExchangeStorageCancel:
			request := item.replicate
			batch.Items[index] = ExchangeItem{RequestID: item.requestID, Kind: item.kind, FundingNonce: item.fundingNonce, Replicate: &request}
		case ExchangeProbe:
			request := item.probe
			batch.Items[index] = ExchangeItem{RequestID: item.requestID, Kind: ExchangeProbe, Probe: &request}
		case ExchangeFetch:
			request := item.fetch
			batch.Items[index] = ExchangeItem{RequestID: item.requestID, Kind: ExchangeFetch, Fetch: &request}
		}
	}
	ctx, cancel := context.WithTimeout(b.cfg.OwnerContext, b.cfg.ExchangeTimeout)
	response, err := callPeerExchange(ctx, b.cfg.Link, node, batch)
	cancel()
	if err != nil {
		for index := range items {
			if items[index].kind == ExchangeReplicate {
				results[index].replicate = ReplicateResult{Status: ReplicateOutcomeUnknown}
			}
			errs[index] = errors.Join(errPeerOutcomeUnknown, err)
		}
		return results, errs
	}
	if response.Version != ExchangeVersion || len(response.Items) != len(items) {
		return invalidExchangeResults(items, results, errs)
	}
	byID := make(map[uint64]ExchangeItemResult, len(response.Items))
	for _, item := range response.Items {
		if item.RequestID == 0 {
			return invalidExchangeResults(items, results, errs)
		}
		if _, exists := byID[item.RequestID]; exists {
			return invalidExchangeResults(items, results, errs)
		}
		byID[item.RequestID] = item
	}
	for index, item := range items {
		result, ok := byID[item.requestID]
		if !ok {
			return invalidExchangeResults(items, results, errs)
		}
		switch item.kind {
		case ExchangeReplicate, ExchangeStoragePrepare, ExchangeStorageCancel:
			if item.kind != ExchangeReplicate {
				if result.Replicate != (ReplicateResult{}) || !zeroProbeResult(result.Probe) || !zeroFetchResult(result.Fetch) || !validStorageFundingResult(item.replicate, item.fundingNonce, result.Funding) {
					return invalidExchangeResults(items, results, errs)
				}
				results[index].funding = result.Funding
				continue
			}
			if result.Funding != (StorageFundingResult{}) || !zeroProbeResult(result.Probe) || !zeroFetchResult(result.Fetch) || !validReplicateResult(item.replicate, result.Replicate) {
				return invalidExchangeResults(items, results, errs)
			}
			results[index].replicate = result.Replicate
		case ExchangeProbe:
			if result.Funding != (StorageFundingResult{}) || result.Replicate != (ReplicateResult{}) || !zeroFetchResult(result.Fetch) || !validPeerProbeResult(item.probe, result.Probe) {
				return invalidExchangeResults(items, results, errs)
			}
			results[index].probe = result.Probe
		case ExchangeFetch:
			if result.Funding != (StorageFundingResult{}) || result.Replicate != (ReplicateResult{}) || !zeroProbeResult(result.Probe) || !validPeerFetchResult(item.fetch, result.Fetch) {
				return invalidExchangeResults(items, results, errs)
			}
			results[index].fetch = result.Fetch
		default:
			return invalidExchangeResults(items, results, errs)
		}
	}
	return results, errs
}

func callPeerExchange(ctx context.Context, link PeerLink, node ch.NodeID, batch ExchangeBatch) (response ExchangeBatchResult, err error) {
	defer func() {
		if recover() != nil {
			response = ExchangeBatchResult{}
			err = errPeerExchangePanic
		}
	}()
	return link.Exchange(ctx, node, batch)
}

func validReplicateResult(request ReplicateRequest, result ReplicateResult) bool {
	switch result.Status {
	case ReplicateDurable, ReplicateAlreadyDurable:
		return result.LastOffset == request.Manifest.LastOffset && result.NeedFrom == 0 && result.Proof == replicateProofFor(request)
	case ReplicateNeedFrom:
		return result.LastOffset == 0 && result.Proof == (ReplicateProof{}) && result.NeedFrom > 0 && result.NeedFrom <= request.Manifest.LastOffset
	default:
		return result.LastOffset == 0 && result.NeedFrom == 0 && result.Proof == (ReplicateProof{})
	}
}

func validPeerProbeResult(request ProbeRequest, result ProbeResult) bool {
	return sameProbeProof(result.Proof, probeProofFor(request)) && validRecoveryProbeResult(result) &&
		sameRecoveryProbeIndexes(request.Indexes, result.Entries)
}

func validPeerFetchResult(request FetchRequest, result FetchResult) bool {
	return result.Proof == fetchProofFor(request) && result.State == request.Expected &&
		validRecoveryProposals(request, result.Proposals)
}

func zeroProbeResult(result ProbeResult) bool {
	return zeroProbeProof(result.Proof) && result.State == (ReplicaState{}) && len(result.Entries) == 0
}

func zeroFetchResult(result FetchResult) bool {
	return result.Proof == (FetchProof{}) && result.State == (ReplicaState{}) && len(result.Proposals) == 0
}

func invalidExchangeResults(items []queuedPeerItem, results []peerExchangeResult, errs []error) ([]peerExchangeResult, []error) {
	for index := range results {
		results[index] = peerExchangeResult{}
		if items[index].kind == ExchangeReplicate {
			results[index].replicate = ReplicateResult{Status: ReplicateOutcomeUnknown}
		}
		errs[index] = errInvalidExchangeResult
	}
	return results, errs
}

// publishCallbacks transfers each terminal envelope before releasing any I/O
// fence. Later same-Channel exchanges cannot publish ahead of this batch.
func (b *peerBatcher) publishCallbacks(node ch.NodeID, items []queuedPeerItem, results []peerExchangeResult, errs []error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.targets[node]
	if target == nil {
		panic("channel replication: terminal result has no retained target")
	}
	if target.callbacks == nil {
		target.callbacks = make(map[ch.ChannelKey]*peerCallbackQueue)
	}
	keys := make([]ch.ChannelKey, len(items))
	for i := range items {
		key := items[i].channelKey()
		keys[i] = key
		queue := target.callbacks[key]
		if queue == nil {
			queue = &peerCallbackQueue{}
			target.callbacks[key] = queue
		}
		envelope := &peerCallbackEnvelope{item: items[i], result: results[i], err: errs[i]}
		queue.items = append(queue.items, envelope)
		items[i] = queuedPeerItem{}
		results[i] = peerExchangeResult{}
		errs[i] = nil
		b.readyCallbackLocked(peerCallbackKey{node: node, channel: key}, queue)
	}
	for _, key := range keys {
		delete(target.inflight, key)
	}
	_ = b.ensureTargetWorkersLocked(node, target)
	_ = b.scheduleCallbacksLocked()
	b.signalProgress()
}

func (b *peerBatcher) readyCallbackLocked(key peerCallbackKey, queue *peerCallbackQueue) {
	if queue.running || queue.ready || len(queue.items) == 0 {
		return
	}
	queue.ready = true
	b.callbackReady = append(b.callbackReady, key)
}

// scheduleCallbacksLocked counts queued and executing tasks alike. Refusal
// restores the exact FIFO head; the supervised retry never loses ownership.
func (b *peerBatcher) scheduleCallbacksLocked() error {
	for b.callbackOwners < b.cfg.CallbackWorkers && len(b.callbackReady) > 0 {
		key := b.callbackReady[0]
		target := b.targets[key.node]
		var queue *peerCallbackQueue
		if target != nil {
			queue = target.callbacks[key.channel]
		}
		if queue == nil || !queue.ready || queue.running || len(queue.items) == 0 {
			b.popReadyCallbackLocked()
			continue
		}
		envelope := queue.items[0]
		queue.running, queue.ready = true, false
		b.callbackOwners++
		if err := b.cfg.CallbackExecutor.Submit(func() { b.runCallback(key, envelope) }); err != nil {
			b.callbackOwners--
			queue.running, queue.ready = false, true
			return err
		}
		queue.items[0] = nil
		queue.items = queue.items[1:]
		if len(queue.items) == 0 {
			queue.items = nil
		}
		b.popReadyCallbackLocked()
	}
	return nil
}

func (b *peerBatcher) popReadyCallbackLocked() {
	b.callbackReady[0] = peerCallbackKey{}
	b.callbackReady = b.callbackReady[1:]
	if len(b.callbackReady) == 0 {
		b.callbackReady = nil
	}
}

// runCallback executes one envelope, never a hot Channel's entire tail. The
// initial lock waits for its admission transaction before invoking user code.
// Callbacks must not synchronously await a later callback on this target+Channel.
func (b *peerBatcher) runCallback(key peerCallbackKey, envelope *peerCallbackEnvelope) {
	b.mu.Lock()
	b.mu.Unlock()
	defer b.finishCallback(key, envelope)
	switch envelope.item.kind {
	case ExchangeReplicate:
		envelope.item.completeReplicate(envelope.result.replicate, envelope.err)
	case ExchangeStoragePrepare, ExchangeStorageCancel:
		envelope.item.completeFunding(envelope.result.funding, envelope.err)
	case ExchangeProbe:
		envelope.item.completeProbe(envelope.result.probe, envelope.err)
	case ExchangeFetch:
		envelope.item.completeFetch(envelope.result.fetch, envelope.err)
	}
}

// finishCallback releases retained bytes only after the callback returns,
// including panic unwinding, then puts this Channel's next head behind cold keys.
func (b *peerBatcher) finishCallback(key peerCallbackKey, envelope *peerCallbackEnvelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bytes := envelope.item.bytes
	*envelope = peerCallbackEnvelope{}
	b.callbackOwners--
	b.ownedItems--
	b.ownedBytes -= bytes
	if target := b.targets[key.node]; target != nil {
		target.ownedItems--
		target.ownedBytes -= bytes
		if queue := target.callbacks[key.channel]; queue != nil {
			queue.running = false
			if len(queue.items) == 0 {
				delete(target.callbacks, key.channel)
			} else {
				b.readyCallbackLocked(key, queue)
			}
		}
		if target.ownedItems == 0 && target.workerCount() == 0 {
			delete(b.targets, key.node)
		}
	}
	_ = b.scheduleCallbacksLocked()
	b.signalProgress()
}

func (b *peerBatcher) submitFunding(ctx context.Context, node ch.NodeID, request ReplicateRequest, nonce uint64, cancel bool, complete func(StorageFundingResult, error)) error {
	if b == nil || ctx == nil || node == 0 || nonce == 0 || complete == nil || !request.Valid() || request.Follower != node {
		return ch.ErrInvalidConfig
	}
	kind := ExchangeStoragePrepare
	if cancel {
		kind = ExchangeStorageCancel
	}
	return b.enqueue(ctx, node, queuedPeerItem{kind: kind, replicate: request, fundingNonce: nonce, bytes: estimateReplicateRequestBytes(request) + 16, completeFunding: complete}, false)
}
func validStorageFundingResult(q ReplicateRequest, nonce uint64, r StorageFundingResult) bool {
	if r.Nonce != nonce || r.Prepared && r.Canceled {
		return false
	}
	if r.Prepared || r.Canceled {
		return r.NeedFrom == 0 && r.Proof == replicateProofFor(q)
	}
	return r.Proof == (ReplicateProof{}) && r.NeedFrom <= q.Manifest.LastOffset
}
