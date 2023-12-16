package raftgroup

import "sync"

type readyRaft struct {
	mu    sync.Mutex
	ready map[uint32]struct{}
	maps  [2]map[uint32]struct{} // 主要目的是为了提升性能，减少map的创建和删除
	index uint8
}

func newReadyRaft() *readyRaft {
	r := &readyRaft{}
	r.maps[0] = make(map[uint32]struct{})
	r.maps[1] = make(map[uint32]struct{})
	r.ready = r.maps[0]
	return r
}

func (r *readyRaft) setRaftReady(shardID uint32) {
	r.mu.Lock()
	r.ready[shardID] = struct{}{}
	r.mu.Unlock()
}

func (r *readyRaft) getReadyRaft() map[uint32]struct{} {
	m := r.maps[(r.index+1)%2]
	for k := range m {
		delete(m, k)
	}
	r.mu.Lock()
	v := r.ready
	r.index++
	r.ready = r.maps[r.index%2]
	r.mu.Unlock()
	return v
}
