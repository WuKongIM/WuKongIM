package multiraft

import "sync"

type scheduler struct {
	ch chan SlotID

	mu         sync.Mutex
	pending    []SlotID
	queued     map[SlotID]struct{}
	processing map[SlotID]struct{}
	dirty      map[SlotID]struct{}
}

func newScheduler() *scheduler {
	return &scheduler{
		ch:         make(chan SlotID, 1024),
		queued:     make(map[SlotID]struct{}),
		processing: make(map[SlotID]struct{}),
		dirty:      make(map[SlotID]struct{}),
	}
}

func (s *scheduler) enqueue(slotID SlotID) {
	s.mu.Lock()
	if _, ok := s.queued[slotID]; ok {
		s.mu.Unlock()
		return
	}
	if _, ok := s.processing[slotID]; ok {
		s.dirty[slotID] = struct{}{}
		s.mu.Unlock()
		return
	}
	s.queued[slotID] = struct{}{}
	s.pending = append(s.pending, slotID)
	s.dispatchLocked()
	s.mu.Unlock()
}

func (s *scheduler) begin(slotID SlotID) {
	s.mu.Lock()
	delete(s.queued, slotID)
	s.processing[slotID] = struct{}{}
	s.dispatchLocked()
	s.mu.Unlock()
}

func (s *scheduler) done(slotID SlotID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.processing, slotID)
	if _, ok := s.dirty[slotID]; !ok {
		return false
	}
	delete(s.dirty, slotID)
	return true
}

func (s *scheduler) requeue(slotID SlotID) {
	s.mu.Lock()
	if _, ok := s.queued[slotID]; ok {
		s.mu.Unlock()
		return
	}
	s.queued[slotID] = struct{}{}
	s.pending = append(s.pending, slotID)
	s.dispatchLocked()
	s.mu.Unlock()
}

func (s *scheduler) dispatchLocked() {
	for len(s.pending) > 0 {
		select {
		case s.ch <- s.pending[0]:
			s.pending = s.pending[1:]
		default:
			return
		}
	}
}
