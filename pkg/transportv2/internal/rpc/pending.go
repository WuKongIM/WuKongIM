package rpc

import "sync"

const defaultPendingShards = 16

// Response carries the payload or terminal error for a pending RPC request.
type Response struct {
	// Payload is the successful response body returned by the remote peer.
	Payload []byte
	// Err is the terminal failure reported for the pending request.
	Err error
}

// PendingTable tracks in-flight RPC requests using sharded maps to reduce lock contention.
type PendingTable struct {
	// shards partitions pending requests so hot request ids do not share one global lock.
	shards []pendingShard
	// mask routes request ids to shards with id & mask.
	mask uint64
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
func (p *PendingTable) Store(id uint64, ch chan Response) {
	shard := p.shardFor(id)
	shard.mu.Lock()
	shard.entries[id] = ch
	shard.mu.Unlock()
}

// Delete removes a pending RPC request without sending a response.
func (p *PendingTable) Delete(id uint64) {
	shard := p.shardFor(id)
	shard.mu.Lock()
	delete(shard.entries, id)
	shard.mu.Unlock()
}

// Complete removes a pending RPC request and sends its response outside the shard lock.
func (p *PendingTable) Complete(id uint64, resp Response) bool {
	shard := p.shardFor(id)
	shard.mu.Lock()
	ch, ok := shard.entries[id]
	if ok {
		delete(shard.entries, id)
	}
	shard.mu.Unlock()

	if !ok {
		return false
	}
	ch <- resp
	return true
}

// FailAll removes all pending RPC requests and sends a non-blocking error response to each.
func (p *PendingTable) FailAll(err error) {
	for i := range p.shards {
		shard := &p.shards[i]
		shard.mu.Lock()
		entries := shard.entries
		shard.entries = make(map[uint64]chan Response)
		shard.mu.Unlock()

		for _, ch := range entries {
			select {
			case ch <- Response{Err: err}:
			default:
			}
		}
	}
}

// Len returns the total number of pending RPC requests across all shards.
func (p *PendingTable) Len() int {
	total := 0
	for i := range p.shards {
		shard := &p.shards[i]
		shard.mu.Lock()
		total += len(shard.entries)
		shard.mu.Unlock()
	}
	return total
}

func (p *PendingTable) shardFor(id uint64) *pendingShard {
	return &p.shards[id&p.mask]
}

func isPowerOfTwo(n int) bool {
	return n > 0 && n&(n-1) == 0
}
