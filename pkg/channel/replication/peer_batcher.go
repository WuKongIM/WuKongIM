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
}

type peerTargetQueue struct {
	urgent            []queuedPeerItem
	hedged            []queuedPeerItem
	deferred          []queuedPeerItem
	background        []queuedPeerItem
	urgentWorkers     int
	hedgedWorkers     int
	backgroundWorkers int
	// hedgeBorrowed counts urgent owners temporarily serving the hedge lane.
	// They remain included in urgentWorkers and therefore in workerCount.
	hedgeBorrowed int
	inflight      map[ch.ChannelKey]struct{}
	ownedItems    int
	ownedBytes    int
}

type peerWorkClass uint8

const (
	peerWorkUrgent peerWorkClass = iota + 1
	peerWorkHedged
	peerWorkBackground

	maxHedgedTargetFlights = 4
)

func (q *peerTargetQueue) workerCount() int {
	if q == nil {
		return 0
	}
	return q.urgentWorkers + q.hedgedWorkers + q.backgroundWorkers
}

type queuedPeerItem struct {
	requestID         uint64
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
	case ExchangeReplicate:
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
	replicate ReplicateResult
	probe     ProbeResult
	fetch     FetchResult
}

func newPeerBatcher(cfg peerBatcherConfig) (*peerBatcher, error) {
	if cfg.MaxTargetFlight == 0 {
		cfg.MaxTargetFlight = defaultRuntimePeerTargetFlight
	}
	if cfg.Link == nil || cfg.Executor == nil || cfg.OwnerContext == nil || cfg.OwnerContext.Err() != nil || cfg.ExchangeTimeout <= 0 ||
		cfg.MaxTargetFlight <= 0 ||
		cfg.MaxBatchItems <= 0 || cfg.MaxBatchBytes <= 0 ||
		cfg.MaxQueuedItems < cfg.MaxBatchItems || cfg.MaxQueuedBytes < cfg.MaxBatchBytes ||
		cfg.MaxTargetQueuedItems < cfg.MaxBatchItems || cfg.MaxTargetQueuedItems > cfg.MaxQueuedItems-cfg.MaxBatchItems ||
		cfg.MaxTargetQueuedBytes < cfg.MaxBatchBytes || cfg.MaxTargetQueuedBytes > cfg.MaxQueuedBytes-cfg.MaxBatchBytes {
		return nil, ch.ErrInvalidConfig
	}
	return &peerBatcher{cfg: cfg, targets: make(map[ch.NodeID]*peerTargetQueue)}, nil
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
	}, peerWorkUrgent)
}

// submitHedged admits a trailing quorum candidate into its own bounded lane.
// Keeping hedge traffic separate prevents a slow preferred follower from
// feeding back into later preferred-follower admission on the same target.
func (b *peerBatcher) submitHedged(ctx context.Context, node ch.NodeID, request ReplicateRequest, complete func(ReplicateResult, error)) error {
	if b == nil || ctx == nil || node == 0 || complete == nil || !request.Valid() || request.Follower != node {
		return ch.ErrInvalidConfig
	}
	return b.enqueue(ctx, node, queuedPeerItem{
		kind: ExchangeReplicate, replicate: request,
		bytes: estimateReplicateRequestBytes(request), completeReplicate: complete,
	}, peerWorkHedged)
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
	}, peerWorkBackground)
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
	}, peerWorkUrgent)
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
	}, peerWorkUrgent)
}

func (b *peerBatcher) enqueue(ctx context.Context, node ch.NodeID, item queuedPeerItem, class peerWorkClass) error {
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
	if b.cfg.OwnerContext.Err() != nil {
		return ch.ErrClosed
	}
	target := b.targets[node]
	if target == nil {
		target = &peerTargetQueue{}
		b.targets[node] = target
	}
	if b.ownedItems >= b.cfg.MaxQueuedItems || b.ownedBytes+item.bytes > b.cfg.MaxQueuedBytes ||
		target.ownedItems >= b.cfg.MaxTargetQueuedItems || target.ownedBytes+item.bytes > b.cfg.MaxTargetQueuedBytes {
		return ch.ErrBackpressured
	}
	item.requestID = b.nextID.Add(1)
	item.queuedAt = time.Now()
	switch class {
	case peerWorkBackground:
		target.deferred = append(target.deferred, item)
	case peerWorkHedged:
		target.hedged = append(target.hedged, item)
	case peerWorkUrgent:
		target.urgent = append(target.urgent, item)
	default:
		return ch.ErrInvalidConfig
	}
	target.ownedItems++
	target.ownedBytes += item.bytes
	b.ownedItems++
	b.ownedBytes += item.bytes
	if class == peerWorkBackground {
		return nil
	}
	if err := b.ensureTargetWorkersLocked(node, target); err != nil {
		workers := target.urgentWorkers
		if class == peerWorkHedged {
			workers = target.hedgedWorkers
		}
		if workers > 0 {
			return nil
		}
		if class == peerWorkHedged {
			target.hedged = target.hedged[:len(target.hedged)-1]
		} else {
			target.urgent = target.urgent[:len(target.urgent)-1]
		}
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
	var flushErr error
	for node, target := range b.targets {
		if len(target.deferred) > 0 {
			target.background = append(target.background, target.deferred...)
			target.deferred = target.deferred[:0]
		}
		if err := b.ensureTargetWorkersLocked(node, target); err != nil {
			flushErr = errors.Join(flushErr, err)
		}
	}
	return flushErr
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
	case peerWorkHedged:
		target.hedgedWorkers++
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
	urgentLimit := b.cfg.MaxTargetFlight
	if target.backgroundWorkers > 0 || len(target.background) > 0 {
		urgentLimit--
	}
	if urgentLimit < 1 {
		urgentLimit = 1
	}
	if len(target.urgent) > 0 && target.urgentWorkers == 0 && target.workerCount() < b.cfg.MaxTargetFlight {
		if err := b.scheduleTargetLocked(node, target, peerWorkUrgent); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
		}
	}
	hedgeLimit := b.hedgeFlightLimit()
	desiredHedgeWorkers := min(hedgeLimit, len(target.hedged))
	for target.hedgedWorkers+target.hedgeBorrowed < desiredHedgeWorkers && target.workerCount() < b.cfg.MaxTargetFlight {
		if err := b.scheduleTargetLocked(node, target, peerWorkHedged); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
			break
		}
	}
	if b.cfg.MaxTargetFlight > 1 && len(target.background) > 0 && target.backgroundWorkers == 0 && target.workerCount() < b.cfg.MaxTargetFlight {
		if err := b.scheduleTargetLocked(node, target, peerWorkBackground); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
		}
	}
	if b.cfg.MaxTargetFlight == 1 && len(target.urgent) == 0 && len(target.hedged) == 0 && len(target.background) > 0 && target.backgroundWorkers == 0 && target.workerCount() == 0 {
		if err := b.scheduleTargetLocked(node, target, peerWorkBackground); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
		}
	}
	if urgentWasScheduled && len(target.urgent) > 0 && len(target.inflight) > 0 &&
		target.urgentWorkers < urgentLimit && target.workerCount() < b.cfg.MaxTargetFlight {
		if err := b.scheduleTargetLocked(node, target, peerWorkUrgent); err != nil {
			scheduleErr = errors.Join(scheduleErr, err)
		}
	}
	return scheduleErr
}

func (b *peerBatcher) hedgeFlightLimit() int {
	limit := min(maxHedgedTargetFlights, b.cfg.MaxTargetFlight)
	if b.cfg.MaxTargetFlight > 1 {
		limit = min(limit, b.cfg.MaxTargetFlight-1)
	}
	return max(1, limit)
}

func (b *peerBatcher) drainTarget(node ch.NodeID, class peerWorkClass) {
	defer b.finishTargetWorker(node, class)
	for {
		items, exchangeClass, borrowedHedge := b.takeBatch(node, class)
		if len(items) == 0 {
			return
		}
		queueStage, exchangeStage, endToEndStage := peerStageNames(exchangeClass)
		exchangeStarted := time.Now()
		for index := range items {
			observeReplicationStage(b.cfg.Observer, queueStage, nil, exchangeStarted.Sub(items[index].queuedAt))
		}
		results, errs := b.exchange(node, exchangeClass, items)
		exchangeFinished := time.Now()
		var exchangeErr error
		for index := range errs {
			if errs[index] != nil {
				exchangeErr = errs[index]
				break
			}
		}
		observeReplicationStage(b.cfg.Observer, exchangeStage, exchangeErr, exchangeFinished.Sub(exchangeStarted))
		b.release(node, items, borrowedHedge)
		for index := range items {
			observeReplicationStage(b.cfg.Observer, endToEndStage, errs[index], exchangeFinished.Sub(items[index].queuedAt))
			switch items[index].kind {
			case ExchangeReplicate:
				items[index].completeReplicate(results[index].replicate, errs[index])
			case ExchangeProbe:
				items[index].completeProbe(results[index].probe, errs[index])
			case ExchangeFetch:
				items[index].completeFetch(results[index].fetch, errs[index])
			}
		}
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
	case peerWorkHedged:
		if target.hedgedWorkers > 0 {
			target.hedgedWorkers--
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
}

func (b *peerBatcher) takeBatch(node ch.NodeID, class peerWorkClass) ([]queuedPeerItem, peerWorkClass, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.targets[node]
	if target == nil {
		return nil, class, false
	}
	borrowedHedge := false
	if class == peerWorkUrgent && len(target.hedged) > 0 && target.hedgedWorkers+target.hedgeBorrowed < b.hedgeFlightLimit() {
		// A saturated preferred lane lends only the bounded flights needed to
		// split queued hedges. Remaining owners stay isolated for preferred work.
		target.hedgeBorrowed++
		class = peerWorkHedged
		borrowedHedge = true
	}
	queue := target.urgent
	switch class {
	case peerWorkHedged:
		queue = target.hedged
	case peerWorkBackground:
		queue = target.background
	}
	if len(queue) == 0 {
		if borrowedHedge {
			target.hedgeBorrowed--
		}
		return nil, class, false
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
			if blockedChannels == nil {
				blockedChannels = make(map[ch.ChannelKey]struct{})
			}
			blockedChannels[channelKey] = struct{}{}
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
	switch class {
	case peerWorkBackground:
		target.background = remaining
	case peerWorkHedged:
		target.hedged = remaining
	default:
		target.urgent = remaining
	}
	return items, class, borrowedHedge
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
		case ExchangeReplicate:
			request := item.replicate
			batch.Items[index] = ExchangeItem{RequestID: item.requestID, Kind: ExchangeReplicate, Replicate: &request}
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
		case ExchangeReplicate:
			if !zeroProbeResult(result.Probe) || !zeroFetchResult(result.Fetch) || !validReplicateResult(item.replicate, result.Replicate) {
				return invalidExchangeResults(items, results, errs)
			}
			results[index].replicate = result.Replicate
		case ExchangeProbe:
			if result.Replicate != (ReplicateResult{}) || !zeroFetchResult(result.Fetch) || !validPeerProbeResult(item.probe, result.Probe) {
				return invalidExchangeResults(items, results, errs)
			}
			results[index].probe = result.Probe
		case ExchangeFetch:
			if result.Replicate != (ReplicateResult{}) || !zeroProbeResult(result.Probe) || !validPeerFetchResult(item.fetch, result.Fetch) {
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

func (b *peerBatcher) release(node ch.NodeID, items []queuedPeerItem, borrowedHedge bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.targets[node]
	if target == nil {
		return
	}
	if borrowedHedge {
		target.hedgeBorrowed--
	}
	for _, item := range items {
		delete(target.inflight, item.channelKey())
		target.ownedItems--
		target.ownedBytes -= item.bytes
		b.ownedItems--
		b.ownedBytes -= item.bytes
	}
}
