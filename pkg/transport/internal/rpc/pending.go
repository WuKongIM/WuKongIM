package rpc

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

const defaultPendingShards = 16
const invalidPendingOwnerPanic = "transport/rpc: pending completion owner must not be nil"

// Response carries the payload or terminal error for a pending RPC request.
type Response struct {
	// Payload is the successful response body returned by the remote peer.
	Payload []byte
	// Err is the terminal failure reported for the pending request.
	Err error
	// ReceiveTiming is immutable after candidate commit. Nil means unknown;
	// it does not change PendingTable ownership or the response's business result.
	ReceiveTiming *ReceiveTiming
}

// ReceiveTiming follows an existing sampled response or inbound frame owner.
// FrameReady follows full frame decoding and trace identity validation. Notified
// is the completed response candidate's owner commit; server requests leave it zero.
type ReceiveTiming struct {
	ServiceID                          uint16
	FrameReady, ObserverDone, Notified time.Time
}

// PendingTable indexes request-owned completions with bounded shard contention.
// No index or close-gate lock is held while waiting for a completion owner.
type PendingTable struct {
	// count changes only with a real insertion/removal while its shard is locked.
	count atomic.Int64
	// shards and mask retain the existing power-of-two request-id partitioning.
	shards []pendingShard
	mask   uint64
	// closeMu fences earlier Stores before FailAll detaches their entries.
	closeMu  sync.RWMutex
	closed   bool
	closeErr error
}

type pendingShard struct {
	mu sync.Mutex
	// entries retain owners while response readers borrow them to copy payloads.
	entries map[uint64]*Completion
}

// NewPendingTable creates a pending RPC table with a power-of-two shard count.
func NewPendingTable(numShards int) *PendingTable {
	if numShards <= 0 || !isPowerOfTwo(numShards) {
		numShards = defaultPendingShards
	}
	p := &PendingTable{shards: make([]pendingShard, numShards), mask: uint64(numShards - 1)}
	for i := range p.shards {
		p.shards[i].entries = make(map[uint64]*Completion)
	}
	return p
}

// Store registers an owner, or settles it outside index locks after shutdown.
// Replacing an id also settles the displaced owner outside both index locks.
func (p *PendingTable) Store(id uint64, owner *Completion) {
	if owner == nil {
		panic(invalidPendingOwnerPanic)
	}
	p.closeMu.RLock()
	if p.closed {
		err := p.closeErr
		p.closeMu.RUnlock()
		owner.Fail(err)
		return
	}
	shard := p.shardFor(id)
	shard.mu.Lock()
	previous, exists := shard.entries[id]
	if !exists {
		p.count.Add(1)
	}
	shard.entries[id] = owner
	shard.mu.Unlock()
	p.closeMu.RUnlock()
	if previous != nil && previous != owner {
		previous.Fail(core.ErrStopped)
	}
}

// Load borrows an owner without removing its reachability during payload copy.
func (p *PendingTable) Load(id uint64) *Completion {
	shard := p.shardFor(id)
	shard.mu.Lock()
	owner := shard.entries[id]
	shard.mu.Unlock()
	return owner
}

// Delete removes a request canceled by the writer before it reached the wire.
func (p *PendingTable) Delete(id uint64) {
	shard := p.shardFor(id)
	shard.mu.Lock()
	if _, exists := shard.entries[id]; exists {
		delete(shard.entries, id)
		p.count.Add(-1)
	}
	shard.mu.Unlock()
}

// DeleteOwner removes only the borrowed owner, preserving a replacement's entry.
func (p *PendingTable) DeleteOwner(id uint64, owner *Completion) bool {
	shard := p.shardFor(id)
	shard.mu.Lock()
	removed := owner != nil && shard.entries[id] == owner
	if removed {
		delete(shard.entries, id)
		p.count.Add(-1)
	}
	shard.mu.Unlock()
	return removed
}

// FailAll closes admission and detaches each shard before settling any owner.
// The first shutdown error remains canonical for concurrent and future Stores.
func (p *PendingTable) FailAll(err error) {
	if err == nil {
		err = core.ErrStopped
	}
	p.closeMu.Lock()
	if !p.closed {
		p.closed = true
		p.closeErr = err
	}
	err = p.closeErr
	p.closeMu.Unlock()
	for i := range p.shards {
		shard := &p.shards[i]
		shard.mu.Lock()
		entries := shard.entries
		shard.entries = nil
		p.count.Add(-int64(len(entries)))
		shard.mu.Unlock()
		for _, owner := range entries {
			owner.Fail(err)
		}
	}
}

// Len returns the actual indexed count without scanning or locking the shards.
func (p *PendingTable) Len() int                         { return int(p.count.Load()) }
func (p *PendingTable) shardFor(id uint64) *pendingShard { return &p.shards[id&p.mask] }
func isPowerOfTwo(n int) bool                            { return n > 0 && n&(n-1) == 0 }
