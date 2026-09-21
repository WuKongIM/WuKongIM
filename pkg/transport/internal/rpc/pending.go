package rpc

import (
	"sync"
	"sync/atomic"
)

const defaultPendingShards = 16
const invalidPendingChannelPanic = "transport/rpc: pending response channel must be buffered"

// Response carries the payload or terminal error for a pending RPC request.
type Response struct {
	// Payload is the successful response body returned by the remote peer.
	Payload []byte
	// Err is the terminal failure reported for the pending request.
	Err error
}

// PendingTable tracks in-flight RPC requests using sharded maps to reduce lock contention.
type PendingTable struct {
	// count tracks actual insertions and removals while each owning shard is locked.
	count atomic.Int64
	// shards partitions pending requests so hot request ids do not share one global lock.
	shards []pendingShard
	// mask routes request ids to shards with id & mask.
	mask uint64
	// closeMu gates Store against FailAll so shutdown cannot leave new entries behind.
	closeMu sync.RWMutex
	// closed records that FailAll has moved the table into terminal failure state.
	closed bool
	// closeErr is delivered to later Store callers after the table is closed.
	closeErr error
}

// pendingShard owns a subset of pending requests routed by request id.
type pendingShard struct {
	// mu protects entries for this shard only.
	mu sync.Mutex
	// entries maps request ids to caller-owned response channels.
	entries map[uint64]chan Response
}

// NewPendingTable creates a pending RPC table with a power-of-two shard count.
func NewPendingTable(numShards int) *PendingTable {
	if numShards <= 0 || !isPowerOfTwo(numShards) {
		numShards = defaultPendingShards
	}

	p := &PendingTable{
		shards: make([]pendingShard, numShards),
		mask:   uint64(numShards - 1),
	}
	for i := range p.shards {
		p.shards[i].entries = make(map[uint64]chan Response)
	}
	return p
}

// Store records the response channel for an in-flight RPC request.
//
// The caller must pass a non-nil buffered channel and must not close it while the
// request is pending. PendingTable never closes caller-owned channels. After
// FailAll closes the table, Store does not add an entry and instead attempts a
// non-blocking error delivery to the provided channel.
func (p *PendingTable) Store(id uint64, ch chan Response) {
	if ch == nil || cap(ch) < 1 {
		panic(invalidPendingChannelPanic)
	}

	p.closeMu.RLock()
	if p.closed {
		err := p.closeErr
		p.closeMu.RUnlock()
		trySend(ch, Response{Err: err})
		return
	}
	shard := p.shardFor(id)
	shard.mu.Lock()
	if _, exists := shard.entries[id]; !exists {
		p.count.Add(1)
	}
	shard.entries[id] = ch
	shard.mu.Unlock()
	p.closeMu.RUnlock()
}

// Delete removes a pending RPC request without sending a response.
func (p *PendingTable) Delete(id uint64) {
	shard := p.shardFor(id)
	shard.mu.Lock()
	if _, exists := shard.entries[id]; exists {
		delete(shard.entries, id)
		p.count.Add(-1)
	}
	shard.mu.Unlock()
}

// Complete removes a pending RPC request and attempts non-blocking response delivery outside the shard lock.
//
// The return value reports whether the id existed and was removed, not whether
// the response was delivered. Delivery is dropped when the caller channel is full.
func (p *PendingTable) Complete(id uint64, resp Response) bool {
	ch, ok := p.Take(id)
	if !ok {
		return false
	}
	trySend(ch, resp)
	return true
}

// Take removes a request and transfers its response channel to the response reader.
// Missing or canceled requests need no response payload allocation.
func (p *PendingTable) Take(id uint64) (chan Response, bool) {
	shard := p.shardFor(id)
	shard.mu.Lock()
	ch, ok := shard.entries[id]
	if ok {
		delete(shard.entries, id)
		p.count.Add(-1)
	}
	shard.mu.Unlock()
	return ch, ok
}

// FailAll closes the table, removes all pending RPC requests, and attempts non-blocking error delivery.
//
// Once FailAll has run, later Store calls are rejected with the same terminal
// error. Error delivery is dropped when a caller channel is full.
func (p *PendingTable) FailAll(err error) {
	p.closeMu.Lock()
	if !p.closed {
		p.closed = true
		p.closeErr = err
	}
	for i := range p.shards {
		shard := &p.shards[i]
		shard.mu.Lock()
		entries := shard.entries
		p.count.Add(-int64(len(entries)))
		shard.entries = make(map[uint64]chan Response)
		shard.mu.Unlock()

		for _, ch := range entries {
			trySend(ch, Response{Err: err})
		}
	}
	p.closeMu.Unlock()
}

// Len returns the current pending count without scanning or locking the shards.
func (p *PendingTable) Len() int {
	return int(p.count.Load())
}

func (p *PendingTable) shardFor(id uint64) *pendingShard {
	return &p.shards[id&p.mask]
}

func isPowerOfTwo(n int) bool {
	return n > 0 && n&(n-1) == 0
}

func trySend(ch chan Response, resp Response) {
	select {
	case ch <- resp:
	default:
	}
}
