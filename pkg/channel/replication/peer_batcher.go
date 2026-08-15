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
	queued     []queuedReplicate
	scheduled  bool
	ownedItems int
	ownedBytes int
}

type queuedReplicate struct {
	requestID uint64
	request   ReplicateRequest
	bytes     int
	complete  func(ReplicateResult, error)
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.cfg.OwnerContext.Err() != nil {
		return ch.ErrClosed
	}
	bytes := estimateReplicateRequestBytes(request)
	if bytes > b.cfg.MaxBatchBytes || bytes > b.cfg.MaxQueuedBytes {
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
	if b.ownedItems >= b.cfg.MaxQueuedItems || b.ownedBytes+bytes > b.cfg.MaxQueuedBytes ||
		target.ownedItems >= b.cfg.MaxTargetQueuedItems || target.ownedBytes+bytes > b.cfg.MaxTargetQueuedBytes {
		return ch.ErrBackpressured
	}
	item := queuedReplicate{requestID: b.nextID.Add(1), request: request, bytes: bytes, complete: complete}
	target.queued = append(target.queued, item)
	target.ownedItems++
	target.ownedBytes += bytes
	b.ownedItems++
	b.ownedBytes += bytes
	if target.scheduled {
		return nil
	}
	target.scheduled = true
	if err := b.cfg.Executor.Submit(func() { b.drainTarget(node) }); err != nil {
		target.queued = target.queued[:len(target.queued)-1]
		target.ownedItems--
		target.ownedBytes -= bytes
		b.ownedItems--
		b.ownedBytes -= bytes
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
			items[index].complete(results[index], errs[index])
		}
	}
}

func (b *peerBatcher) takeBatch(node ch.NodeID) []queuedReplicate {
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
	for count < len(target.queued) && count < b.cfg.MaxBatchItems {
		next := target.queued[count].bytes
		if count > 0 && bytes+next > b.cfg.MaxBatchBytes {
			break
		}
		bytes += next
		count++
	}
	items := append([]queuedReplicate(nil), target.queued[:count]...)
	copy(target.queued, target.queued[count:])
	target.queued = target.queued[:len(target.queued)-count]
	return items
}

func (b *peerBatcher) exchange(node ch.NodeID, items []queuedReplicate) ([]ReplicateResult, []error) {
	results := make([]ReplicateResult, len(items))
	errs := make([]error, len(items))
	batch := ExchangeBatch{Version: ExchangeVersion, Items: make([]ExchangeItem, len(items))}
	for index, item := range items {
		request := item.request
		batch.Items[index] = ExchangeItem{RequestID: item.requestID, Kind: ExchangeReplicate, Replicate: &request}
	}
	ctx, cancel := context.WithTimeout(b.cfg.OwnerContext, b.cfg.ExchangeTimeout)
	response, err := callPeerExchange(ctx, b.cfg.Link, node, batch)
	cancel()
	if err != nil {
		for index := range items {
			results[index] = ReplicateResult{Status: ReplicateOutcomeUnknown}
			errs[index] = errors.Join(errPeerOutcomeUnknown, err)
		}
		return results, errs
	}
	if response.Version != ExchangeVersion || len(response.Items) != len(items) {
		return invalidExchangeResults(results, errs)
	}
	byID := make(map[uint64]ReplicateResult, len(response.Items))
	for _, item := range response.Items {
		if item.RequestID == 0 || !item.Replicate.Status.Valid() {
			return invalidExchangeResults(results, errs)
		}
		if _, exists := byID[item.RequestID]; exists {
			return invalidExchangeResults(results, errs)
		}
		byID[item.RequestID] = item.Replicate
	}
	for index, item := range items {
		result, ok := byID[item.requestID]
		if !ok || !validReplicateResult(item.request, result) {
			return invalidExchangeResults(results, errs)
		}
		results[index] = result
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

func invalidExchangeResults(results []ReplicateResult, errs []error) ([]ReplicateResult, []error) {
	for index := range results {
		results[index] = ReplicateResult{Status: ReplicateOutcomeUnknown}
		errs[index] = errInvalidExchangeResult
	}
	return results, errs
}

func (b *peerBatcher) release(node ch.NodeID, items []queuedReplicate) {
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
