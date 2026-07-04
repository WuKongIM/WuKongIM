package transport

import "sync"

type pendingMap struct {
	shards []pendingShard
	mask   uint64
}

type pendingShard struct {
	mu sync.Mutex
	m  map[uint64]chan rpcResponse
}

func newPendingMap(numShards int) *pendingMap {
	if numShards <= 0 || numShards&(numShards-1) != 0 {
		numShards = 16
	}
	shards := make([]pendingShard, numShards)
	for i := range shards {
		shards[i].m = make(map[uint64]chan rpcResponse)
	}
	return &pendingMap{
		shards: shards,
		mask:   uint64(numShards - 1),
	}
}

func (p *pendingMap) shard(id uint64) *pendingShard {
	return &p.shards[id&p.mask]
}

func (p *pendingMap) Store(id uint64, ch chan rpcResponse) {
	shard := p.shard(id)
	shard.mu.Lock()
	shard.m[id] = ch
	shard.mu.Unlock()
}

func (p *pendingMap) LoadAndDelete(id uint64) (chan rpcResponse, bool) {
	shard := p.shard(id)
	shard.mu.Lock()
	ch, ok := shard.m[id]
	if ok {
		delete(shard.m, id)
	}
	shard.mu.Unlock()
	return ch, ok
}

func (p *pendingMap) Delete(id uint64) {
	_, _ = p.LoadAndDelete(id)
}

func (p *pendingMap) Range(fn func(uint64, chan rpcResponse)) {
	for i := range p.shards {
		shard := &p.shards[i]
		shard.mu.Lock()
		snapshot := make(map[uint64]chan rpcResponse, len(shard.m))
		for id, ch := range shard.m {
			snapshot[id] = ch
		}
		shard.mu.Unlock()

		for id, ch := range snapshot {
			fn(id, ch)
		}
	}
}
