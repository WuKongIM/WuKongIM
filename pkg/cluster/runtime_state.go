package cluster

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type runtimeState struct {
	mu    sync.RWMutex
	peers map[multiraft.SlotID][]multiraft.NodeID
}

func newRuntimeState() *runtimeState {
	return &runtimeState{
		peers: make(map[multiraft.SlotID][]multiraft.NodeID),
	}
}

func (s *runtimeState) Set(slotID multiraft.SlotID, peers []multiraft.NodeID) {
	if s == nil {
		return
	}
	copyPeers := append([]multiraft.NodeID(nil), peers...)
	s.mu.Lock()
	if s.peers == nil {
		s.peers = make(map[multiraft.SlotID][]multiraft.NodeID)
	}
	s.peers[slotID] = copyPeers
	s.mu.Unlock()
}

func (s *runtimeState) Get(slotID multiraft.SlotID) ([]multiraft.NodeID, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	peers, ok := s.peers[slotID]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return append([]multiraft.NodeID(nil), peers...), true
}

func (s *runtimeState) Delete(slotID multiraft.SlotID) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.peers, slotID)
	s.mu.Unlock()
}

func (s *runtimeState) Snapshot() map[multiraft.SlotID][]multiraft.NodeID {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[multiraft.SlotID][]multiraft.NodeID, len(s.peers))
	for slotID, peers := range s.peers {
		out[slotID] = append([]multiraft.NodeID(nil), peers...)
	}
	return out
}
