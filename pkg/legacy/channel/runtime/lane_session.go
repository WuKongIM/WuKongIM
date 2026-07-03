package runtime

import (
	"sync"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type laneReadyMask uint8

const (
	laneReadyData laneReadyMask = 1 << iota
	laneReadyHWOnly
	laneReadyTruncate
)

type LeaderLaneReadyItem struct {
	ChannelKey   core.ChannelKey
	ChannelEpoch uint64
	// ChannelGeneration is the follower-local generation echoed back with this ready item.
	ChannelGeneration uint64
	ReadyMask         laneReadyMask
	SizeBytes         int
	Response          LaneResponseItem
}

type LeaderLanePollResult struct {
	Items     []LeaderLaneReadyItem
	MoreReady bool
}

type lanePollWaiter struct {
	ready chan struct{}
	once  sync.Once
}

func newLanePollWaiter() *lanePollWaiter {
	return &lanePollWaiter{ready: make(chan struct{})}
}

func (w *lanePollWaiter) Ready() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.ready
}

func (w *lanePollWaiter) wake() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		close(w.ready)
	})
}

type LeaderLaneSession struct {
	mu sync.Mutex

	sessionID    uint64
	sessionEpoch uint64
	readyFlags   map[core.ChannelKey]laneReadyMask
	readyQueue   []core.ChannelKey
	parked       *lanePollWaiter
	dataWake     *time.Timer
	dataWaiter   *lanePollWaiter
	channelEpoch map[core.ChannelKey]uint64
	channelGen   map[core.ChannelKey]uint64
	cursor       map[core.ChannelKey]LaneCursorDelta
}

func newLeaderLaneSession(sessionID, sessionEpoch uint64) *LeaderLaneSession {
	return &LeaderLaneSession{
		sessionID:    sessionID,
		sessionEpoch: sessionEpoch,
		readyFlags:   make(map[core.ChannelKey]laneReadyMask),
		channelEpoch: make(map[core.ChannelKey]uint64),
		channelGen:   make(map[core.ChannelKey]uint64),
		cursor:       make(map[core.ChannelKey]LaneCursorDelta),
	}
}

func (s *LeaderLaneSession) TrackChannel(key core.ChannelKey, epoch uint64, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelEpoch[key] = epoch
	s.channelGen[key] = generation
}

func (s *LeaderLaneSession) ForgetChannel(key core.ChannelKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.channelEpoch, key)
	delete(s.channelGen, key)
	delete(s.cursor, key)
	delete(s.readyFlags, key)
	filtered := s.readyQueue[:0]
	for _, queued := range s.readyQueue {
		if queued == key {
			continue
		}
		filtered = append(filtered, queued)
	}
	s.readyQueue = filtered
}

func (s *LeaderLaneSession) Cursor(key core.ChannelKey) (LaneCursorDelta, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delta, ok := s.cursor[key]
	return delta, ok
}

func (s *LeaderLaneSession) Session() (uint64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID, s.sessionEpoch
}

func (s *LeaderLaneSession) MarkDataReady(key core.ChannelKey, epoch uint64) {
	s.markReady(key, epoch, laneReadyData)
}

func (s *LeaderLaneSession) MarkDataReadyDelayed(key core.ChannelKey, epoch uint64, delay time.Duration) {
	if delay <= 0 {
		s.MarkDataReady(key, epoch)
		return
	}
	s.markReadyDelayed(key, epoch, laneReadyData, delay)
}

func (s *LeaderLaneSession) MarkHWOnlyReady(key core.ChannelKey, epoch uint64) {
	s.markReady(key, epoch, laneReadyHWOnly)
}

func (s *LeaderLaneSession) markReady(key core.ChannelKey, epoch uint64, mask laneReadyMask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if epoch != 0 {
		s.channelEpoch[key] = epoch
	}
	if existing := s.readyFlags[key]; existing != 0 {
		s.readyFlags[key] = existing | mask
		return
	}
	s.readyFlags[key] = mask
	s.readyQueue = append(s.readyQueue, key)
	if s.parked != nil {
		waiter := s.parked
		s.parked = nil
		s.stopDataWakeLocked()
		waiter.wake()
	}
}

func (s *LeaderLaneSession) markReadyDelayed(key core.ChannelKey, epoch uint64, mask laneReadyMask, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if epoch != 0 {
		s.channelEpoch[key] = epoch
	}
	if existing := s.readyFlags[key]; existing != 0 {
		s.readyFlags[key] = existing | mask
		return
	}
	s.readyFlags[key] = mask
	s.readyQueue = append(s.readyQueue, key)
	if s.parked == nil || s.dataWake != nil {
		return
	}
	waiter := s.parked
	s.dataWaiter = waiter
	s.dataWake = time.AfterFunc(delay, func() {
		s.fireDelayedDataWake(waiter)
	})
}

func (s *LeaderLaneSession) fireDelayedDataWake(waiter *lanePollWaiter) {
	s.mu.Lock()
	if s.dataWaiter != waiter {
		s.mu.Unlock()
		return
	}
	s.dataWake = nil
	s.dataWaiter = nil
	if s.parked != waiter {
		s.mu.Unlock()
		return
	}
	s.parked = nil
	s.mu.Unlock()
	waiter.wake()
}

func (s *LeaderLaneSession) stopDataWakeLocked() {
	if s.dataWake != nil {
		s.dataWake.Stop()
	}
	s.dataWake = nil
	s.dataWaiter = nil
}

func (s *LeaderLaneSession) Poll(
	cursor []LaneCursorDelta,
	apply func(LaneCursorDelta),
	budget LanePollBudget,
	selector func(core.ChannelKey, LaneCursorDelta, laneReadyMask) (LeaderLaneReadyItem, bool),
) (LeaderLanePollResult, *lanePollWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, delta := range cursor {
		if trackedEpoch, ok := s.channelEpoch[delta.ChannelKey]; ok && trackedEpoch != 0 && delta.ChannelEpoch != 0 && trackedEpoch != delta.ChannelEpoch {
			continue
		}
		if trackedGeneration, ok := s.channelGen[delta.ChannelKey]; ok && trackedGeneration != 0 && delta.ChannelGeneration != 0 && trackedGeneration != delta.ChannelGeneration {
			continue
		}
		if delta.ChannelEpoch == 0 {
			delta.ChannelEpoch = s.channelEpoch[delta.ChannelKey]
		}
		if delta.ChannelGeneration == 0 {
			delta.ChannelGeneration = s.channelGen[delta.ChannelKey]
		}
		s.cursor[delta.ChannelKey] = delta
		if apply != nil {
			apply(delta)
		}
	}

	if len(s.readyQueue) == 0 {
		waiter := newLanePollWaiter()
		s.parked = waiter
		return LeaderLanePollResult{}, waiter
	}
	s.stopDataWakeLocked()

	if budget.MaxChannels <= 0 {
		budget.MaxChannels = 1
	}

	var (
		result       LeaderLanePollResult
		drainedBytes int
		nextQueue    []core.ChannelKey
		requeueTail  []core.ChannelKey
	)
	for _, key := range s.readyQueue {
		mask := s.readyFlags[key]
		if mask == 0 {
			continue
		}
		cursor := s.cursor[key]
		if cursor.ChannelEpoch == 0 {
			cursor.ChannelEpoch = s.channelEpoch[key]
		}
		if cursor.ChannelGeneration == 0 {
			cursor.ChannelGeneration = s.channelGen[key]
		}
		item, finished := selector(key, cursor, mask)
		if budget.MaxBytes > 0 && len(result.Items) > 0 && drainedBytes+item.SizeBytes > budget.MaxBytes {
			nextQueue = append(nextQueue, key)
			result.MoreReady = true
			continue
		}
		if len(result.Items) >= budget.MaxChannels {
			nextQueue = append(nextQueue, key)
			result.MoreReady = true
			continue
		}
		result.Items = append(result.Items, item)
		drainedBytes += item.SizeBytes
		if finished {
			delete(s.readyFlags, key)
			continue
		}
		requeueTail = append(requeueTail, key)
	}
	nextQueue = append(nextQueue, requeueTail...)
	if len(nextQueue) > 0 {
		result.MoreReady = true
	}
	s.readyQueue = nextQueue
	return result, nil
}
