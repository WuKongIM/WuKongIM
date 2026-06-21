package multiraft

import "sync"

type scheduler struct {
	ch chan SlotID

	observer SchedulerObserver

	mu          sync.Mutex
	pending     []SlotID
	pendingHead int
	queued      map[SlotID]struct{}
	processing  map[SlotID]struct{}
	dirty       map[SlotID]struct{}
}

type schedulerObservation struct {
	admission string
	state     SchedulerStateEvent
	hasState  bool
}

func newScheduler(observer SchedulerObserver) *scheduler {
	return &scheduler{
		observer:   observer,
		ch:         make(chan SlotID, 1024),
		queued:     make(map[SlotID]struct{}),
		processing: make(map[SlotID]struct{}),
		dirty:      make(map[SlotID]struct{}),
	}
}

func (s *scheduler) enqueue(slotID SlotID) {
	s.mu.Lock()
	if _, ok := s.queued[slotID]; ok {
		obs := s.observationLocked("coalesced")
		s.mu.Unlock()
		s.emitObservation(obs)
		return
	}
	if _, ok := s.processing[slotID]; ok {
		s.dirty[slotID] = struct{}{}
		obs := s.observationLocked("dirty")
		s.mu.Unlock()
		s.emitObservation(obs)
		return
	}
	s.queued[slotID] = struct{}{}
	s.pending = append(s.pending, slotID)
	s.dispatchLocked()
	obs := s.observationLocked("ok")
	s.mu.Unlock()
	s.emitObservation(obs)
}

func (s *scheduler) begin(slotID SlotID) {
	s.mu.Lock()
	delete(s.queued, slotID)
	s.processing[slotID] = struct{}{}
	s.dispatchLocked()
	obs := s.observationLocked("")
	s.mu.Unlock()
	s.emitObservation(obs)
}

func (s *scheduler) done(slotID SlotID) bool {
	s.mu.Lock()

	delete(s.processing, slotID)
	if _, ok := s.dirty[slotID]; !ok {
		obs := s.observationLocked("")
		s.mu.Unlock()
		s.emitObservation(obs)
		return false
	}
	delete(s.dirty, slotID)
	obs := s.observationLocked("")
	s.mu.Unlock()
	s.emitObservation(obs)
	return true
}

func (s *scheduler) requeue(slotID SlotID) {
	s.mu.Lock()
	if _, ok := s.queued[slotID]; ok {
		obs := s.observationLocked("")
		s.mu.Unlock()
		s.emitObservation(obs)
		return
	}
	s.queued[slotID] = struct{}{}
	s.pending = append(s.pending, slotID)
	s.dispatchLocked()
	obs := s.observationLocked("requeued")
	s.mu.Unlock()
	s.emitObservation(obs)
}

func (s *scheduler) dispatchLocked() {
	for s.pendingLenLocked() > 0 {
		slotID := s.pending[s.pendingHead]
		select {
		case s.ch <- slotID:
			s.pending[s.pendingHead] = 0
			s.pendingHead++
			s.compactPendingLocked()
		default:
			return
		}
	}
}

func (s *scheduler) pendingLenLocked() int {
	return len(s.pending) - s.pendingHead
}

func (s *scheduler) compactPendingLocked() {
	if s.pendingHead == 0 {
		return
	}
	if s.pendingHead >= len(s.pending) {
		s.pending = s.pending[:0]
		s.pendingHead = 0
		return
	}
	if s.pendingHead <= 64 || s.pendingHead*2 < len(s.pending) {
		return
	}
	remaining := copy(s.pending, s.pending[s.pendingHead:])
	clear(s.pending[remaining:])
	s.pending = s.pending[:remaining]
	s.pendingHead = 0
}

func (s *scheduler) observationLocked(admission string) schedulerObservation {
	if s.observer == nil {
		return schedulerObservation{}
	}
	return schedulerObservation{
		admission: admission,
		state: SchedulerStateEvent{
			Depth:      len(s.ch),
			Capacity:   cap(s.ch),
			Pending:    s.pendingLenLocked(),
			Queued:     len(s.queued),
			Processing: len(s.processing),
			Dirty:      len(s.dirty),
		},
		hasState: true,
	}
}

func (s *scheduler) emitObservation(obs schedulerObservation) {
	if s.observer == nil {
		return
	}
	if obs.admission != "" {
		s.observer.ObserveSchedulerAdmission(obs.admission)
	}
	if obs.hasState {
		s.observer.SetSchedulerState(obs.state)
	}
}
