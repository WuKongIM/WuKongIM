package runtime

import (
	"sync"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type snapshotThrottle interface {
	Wait(bytes int64)
}

type fixedRateSnapshotThrottle struct {
	bytesPerSecond int64
	sleep          func(time.Duration)
}

func newSnapshotThrottle(bytesPerSecond int64, sleep func(time.Duration)) snapshotThrottle {
	return fixedRateSnapshotThrottle{
		bytesPerSecond: bytesPerSecond,
		sleep:          sleep,
	}
}

func (t fixedRateSnapshotThrottle) Wait(bytes int64) {
	if t.bytesPerSecond <= 0 || bytes <= 0 {
		return
	}
	delay := time.Duration(bytes*int64(time.Second)) / time.Duration(t.bytesPerSecond)
	if delay <= 0 {
		delay = time.Second
	}
	if t.sleep != nil {
		t.sleep(delay)
	}
}

type snapshotState struct {
	mu            sync.Mutex
	inflight      int
	maxConcurrent int
	waiting       []core.ChannelKey
	waitingSet    map[core.ChannelKey]struct{}
}

func (s *snapshotState) begin(limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit > 0 && s.inflight >= limit {
		return false
	}
	s.inflight++
	if s.inflight > s.maxConcurrent {
		s.maxConcurrent = s.inflight
	}
	return true
}

func (s *snapshotState) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight > 0 {
		s.inflight--
	}
}

func (s *snapshotState) wait(key core.ChannelKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.waitingSet == nil {
		s.waitingSet = make(map[core.ChannelKey]struct{})
	}
	if _, exists := s.waitingSet[key]; exists {
		return
	}
	s.waiting = append(s.waiting, key)
	s.waitingSet[key] = struct{}{}
}

func (s *snapshotState) popWaiter() (core.ChannelKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiting) == 0 {
		return "", false
	}
	key := s.waiting[0]
	s.waiting = s.waiting[1:]
	delete(s.waitingSet, key)
	return key, true
}

func (s *snapshotState) removeWaiter(key core.ChannelKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waiting) == 0 {
		return false
	}
	if s.waitingSet == nil {
		return false
	}
	if _, exists := s.waitingSet[key]; !exists {
		return false
	}
	delete(s.waitingSet, key)
	filtered := make([]core.ChannelKey, 0, len(s.waiting)-1)
	removed := false
	for _, waiter := range s.waiting {
		if waiter == key {
			removed = true
			continue
		}
		filtered = append(filtered, waiter)
	}
	s.waiting = filtered
	return removed
}

func (s *snapshotState) maxObserved() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxConcurrent
}

func (s *snapshotState) waitingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waiting)
}

func (s *snapshotState) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight = 0
	s.waiting = nil
	s.waitingSet = nil
}

func (r *runtime) OnSnapshot(key core.ChannelKey) {
	if r.isClosed() {
		return
	}
	r.processSnapshot(key)
}

func (r *runtime) queueSnapshot(key core.ChannelKey) {
	r.queueSnapshotChunk(key, 0)
}

func (r *runtime) queueSnapshotChunk(key core.ChannelKey, bytes int64) {
	if r.isClosed() {
		return
	}
	ch, ok := r.lookupChannel(key)
	if !ok {
		return
	}
	ch.enqueueSnapshot(bytes)
	ch.markSnapshot()
	r.enqueueScheduler(key, PriorityLow)
}

func (r *runtime) processSnapshot(key core.ChannelKey) {
	if r.isClosed() {
		return
	}
	ch, ok := r.lookupChannel(key)
	if !ok {
		return
	}

	if !r.snapshots.begin(r.cfg.Limits.MaxSnapshotInflight) {
		r.snapshots.wait(key)
		return
	}

	bytes := ch.drainSnapshotBytes()
	if bytes <= 0 {
		r.completeSnapshot(key)
		return
	}
	go r.throttleSnapshot(key, bytes)
}

func (r *runtime) throttleSnapshot(key core.ChannelKey, bytes int64) {
	if r.isClosed() {
		return
	}
	r.snapshotThrottle.Wait(bytes)
	r.completeSnapshot(key)
}

func (r *runtime) maxSnapshotConcurrent() int {
	return r.snapshots.maxObserved()
}

func (r *runtime) completeSnapshot(_ core.ChannelKey) {
	if r.isClosed() {
		return
	}
	r.snapshots.finish()
	for {
		nextKey, ok := r.snapshots.popWaiter()
		if !ok {
			return
		}
		next, exists := r.lookupChannel(nextKey)
		if !exists {
			continue
		}
		next.markSnapshot()
		r.enqueueScheduler(nextKey, PriorityLow)
		return
	}
}

func (r *runtime) queuedSnapshotGroups() int {
	return r.snapshots.waitingCount()
}
