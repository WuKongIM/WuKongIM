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
	// OwnerContext and ExchangeTimeout bound accepted work independently from
	// the caller that submitted it.
	OwnerContext    context.Context
	ExchangeTimeout time.Duration

	MaxBatchItems  int
	MaxBatchBytes  int
	MaxQueuedItems int
	MaxQueuedBytes int
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
	queued     []queuedPeerItem
	scheduled  bool
	ownedItems int
	ownedBytes int
}

type queuedPeerItem struct {
	requestID         uint64
	kind              ExchangeKind
	replicate         ReplicateRequest
	probe             ProbeRequest
	bytes             int
	completeReplicate func(ReplicateResult, error)
	completeProbe     func(ProbeResult, error)
}

type peerExchangeResult struct {
	replicate ReplicateResult
	probe     ProbeResult
}

func newPeerBatcher(cfg peerBatcherConfig) (*peerBatcher, error) {
	if cfg.Link == nil || cfg.Executor == nil || cfg.OwnerContext == nil || cfg.OwnerContext.Err() != nil || cfg.ExchangeTimeout <= 0 ||
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
	})
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
	})
}

func (b *peerBatcher) enqueue(ctx context.Context, node ch.NodeID, item queuedPeerItem) error {
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
	target.queued = append(target.queued, item)
	target.ownedItems++
	target.ownedBytes += item.bytes
	b.ownedItems++
	b.ownedBytes += item.bytes
	if target.scheduled {
		return nil
	}
	target.scheduled = true
	if err := b.cfg.Executor.Submit(func() { b.drainTarget(node) }); err != nil {
		target.queued = target.queued[:len(target.queued)-1]
		target.ownedItems--
		target.ownedBytes -= item.bytes
		b.ownedItems--
		b.ownedBytes -= item.bytes
		target.scheduled = false
		if target.ownedItems == 0 {
			delete(b.targets, node)
		}
		return err
	}
	return nil
}

func (b *peerBatcher) drainTarget(node ch.NodeID) {
	for {
		items := b.takeBatch(node)
		if len(items) == 0 {
			return
		}
		results, errs := b.exchange(node, items)
		b.release(node, items)
		for index := range items {
			switch items[index].kind {
			case ExchangeReplicate:
				items[index].completeReplicate(results[index].replicate, errs[index])
			case ExchangeProbe:
				items[index].completeProbe(results[index].probe, errs[index])
			}
		}
	}
}

func (b *peerBatcher) takeBatch(node ch.NodeID) []queuedPeerItem {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.targets[node]
	if target == nil || len(target.queued) == 0 {
		if target != nil {
			target.scheduled = false
			if target.ownedItems == 0 {
				delete(b.targets, node)
			}
		}
		return nil
	}
	count := 0
	bytes := 0
	kind := target.queued[0].kind
	for count < len(target.queued) && count < b.cfg.MaxBatchItems {
		if target.queued[count].kind != kind {
			break
		}
		next := target.queued[count].bytes
		if count > 0 && bytes+next > b.cfg.MaxBatchBytes {
			break
		}
		bytes += next
		count++
	}
	items := append([]queuedPeerItem(nil), target.queued[:count]...)
	copy(target.queued, target.queued[count:])
	target.queued = target.queued[:len(target.queued)-count]
	return items
}

func (b *peerBatcher) exchange(node ch.NodeID, items []queuedPeerItem) ([]peerExchangeResult, []error) {
	results := make([]peerExchangeResult, len(items))
	errs := make([]error, len(items))
	batch := ExchangeBatch{Version: ExchangeVersion, Items: make([]ExchangeItem, len(items))}
	for index, item := range items {
		switch item.kind {
		case ExchangeReplicate:
			request := item.replicate
			batch.Items[index] = ExchangeItem{RequestID: item.requestID, Kind: ExchangeReplicate, Replicate: &request}
		case ExchangeProbe:
			request := item.probe
			batch.Items[index] = ExchangeItem{RequestID: item.requestID, Kind: ExchangeProbe, Probe: &request}
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
			if !zeroProbeResult(result.Probe) || !validReplicateResult(item.replicate, result.Replicate) {
				return invalidExchangeResults(items, results, errs)
			}
			results[index].replicate = result.Replicate
		case ExchangeProbe:
			if result.Replicate != (ReplicateResult{}) || !validPeerProbeResult(item.probe, result.Probe) {
				return invalidExchangeResults(items, results, errs)
			}
			results[index].probe = result.Probe
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

func zeroProbeResult(result ProbeResult) bool {
	return zeroProbeProof(result.Proof) && result.State == (ReplicaState{}) && len(result.Entries) == 0
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

func (b *peerBatcher) release(node ch.NodeID, items []queuedPeerItem) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.targets[node]
	if target == nil {
		return
	}
	for _, item := range items {
		target.ownedItems--
		target.ownedBytes -= item.bytes
		b.ownedItems--
		b.ownedBytes -= item.bytes
	}
}
