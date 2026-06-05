// Package sched provides byte-aware priority scheduling for transport frames.
package sched

import (
	"context"
	"math"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

const (
	defaultMaxBatchFrames = 64
	defaultMaxBatchBytes  = 1 << 20
)

// Config bounds scheduler queue depth and write batch size.
type Config struct {
	// MaxItems is the maximum queued frame count; non-positive means unlimited.
	MaxItems int
	// MaxBytes is the maximum queued byte count; non-positive means unlimited.
	MaxBytes int64
	// MaxBatchFrames is the maximum frame count returned in one batch.
	MaxBatchFrames int
	// MaxBatchBytes is the maximum byte count returned in one batch.
	MaxBatchBytes int
}

// Item is one schedulable transport frame.
type Item struct {
	// Priority selects the weighted scheduler lane.
	Priority core.Priority
	// Bytes is the queue and batch byte size; negative values are treated as zero.
	Bytes int
	// Value carries the caller-owned frame payload or metadata.
	Value any
}

type lane struct {
	priority core.Priority
	weight   int64
	deficit  int64
	queue    []Item
}

// Scheduler is a byte-aware weighted priority queue.
type Scheduler struct {
	mu   sync.Mutex
	cond *sync.Cond

	maxItems       int
	maxBytes       int64
	maxBatchFrames int
	maxBatchBytes  int64

	lanes       []lane
	nextLane    int
	roundOpen   bool
	roundSeen   int
	roundOutput bool
	queuedItems int
	queuedBytes int64
	stopped     bool
	stopErr     error
}

// New creates a scheduler with configured queue limits and default batch limits.
func New(cfg Config) *Scheduler {
	maxBatchFrames := cfg.MaxBatchFrames
	if maxBatchFrames <= 0 {
		maxBatchFrames = defaultMaxBatchFrames
	}
	maxBatchBytes := cfg.MaxBatchBytes
	if maxBatchBytes <= 0 {
		maxBatchBytes = defaultMaxBatchBytes
	}

	s := &Scheduler{
		maxItems:       cfg.MaxItems,
		maxBytes:       cfg.MaxBytes,
		maxBatchFrames: maxBatchFrames,
		maxBatchBytes:  int64(maxBatchBytes),
		lanes: []lane{
			{priority: core.PriorityRaft, weight: 8},
			{priority: core.PriorityControl, weight: 6},
			{priority: core.PriorityRPC, weight: 4},
			{priority: core.PriorityBulk, weight: 1},
		},
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Enqueue adds an item unless the context is canceled, the scheduler is stopped, or limits are full.
func (s *Scheduler) Enqueue(ctx context.Context, item Item) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := item.Priority.Validate(); err != nil {
		return err
	}
	if item.Bytes < 0 {
		item.Bytes = 0
	}
	if int64(item.Bytes) > s.maxBatchBytes {
		return core.ErrMsgTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return core.ErrStopped
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.maxItems > 0 && s.queuedItems+1 > s.maxItems {
		return core.ErrQueueFull
	}
	if s.maxBytes > 0 && s.queuedBytes+int64(item.Bytes) > s.maxBytes {
		return core.ErrQueueFull
	}

	for i := range s.lanes {
		if s.lanes[i].priority == item.Priority {
			s.lanes[i].queue = append(s.lanes[i].queue, item)
			s.queuedItems++
			s.queuedBytes += int64(item.Bytes)
			s.cond.Signal()
			return nil
		}
	}
	return core.ErrInvalidPriority
}

// NextBatch returns the next weighted batch without blocking.
func (s *Scheduler) NextBatch() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.queuedItems == 0 {
		return nil
	}
	return s.nextBatchLocked()
}

// WaitBatch blocks until a batch is available or the scheduler is stopped.
func (s *Scheduler) WaitBatch() ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for s.queuedItems == 0 && !s.stopped {
		s.cond.Wait()
	}
	if s.stopped {
		return nil, s.stopErr
	}
	return s.nextBatchLocked(), nil
}

// Stop marks the scheduler stopped, drains queued items back to caller ownership, and wakes all waiters.
func (s *Scheduler) Stop(err error) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		s.cond.Broadcast()
		return nil
	}
	if err == nil {
		err = core.ErrStopped
	}
	s.stopped = true
	s.stopErr = err
	drained := s.drainLocked()
	s.cond.Broadcast()
	return drained
}

func (s *Scheduler) nextBatchLocked() []Item {
	var batch []Item
	var batchBytes int64

	for len(batch) < s.maxBatchFrames && s.queuedItems > 0 {
		s.openRoundLocked()

		l := &s.lanes[s.nextLane]
		if len(l.queue) == 0 {
			s.finishLaneLocked()
			continue
		}

		item := l.queue[0]
		itemCost := scheduleCost(item)
		itemBytes := queueBytes(item)
		if itemCost > l.deficit {
			s.finishLaneLocked()
			continue
		}
		if batchBytes+itemBytes > s.maxBatchBytes {
			break
		}

		l.queue[0] = Item{}
		l.queue = l.queue[1:]
		l.deficit -= itemCost
		s.queuedItems--
		s.queuedBytes -= itemBytes
		batch = append(batch, item)
		batchBytes += itemBytes
		s.roundOutput = true

		if len(batch) >= s.maxBatchFrames || batchBytes >= s.maxBatchBytes {
			break
		}
		if len(l.queue) == 0 {
			s.finishLaneLocked()
		}
	}

	return batch
}

func (s *Scheduler) openRoundLocked() {
	if s.roundOpen {
		return
	}
	for i := range s.lanes {
		if len(s.lanes[i].queue) > 0 {
			s.lanes[i].deficit += s.lanes[i].weight
		}
	}
	s.roundOpen = true
	s.roundSeen = 0
	s.roundOutput = false
}

func (s *Scheduler) finishLaneLocked() {
	s.nextLane = (s.nextLane + 1) % len(s.lanes)
	s.roundSeen++
	if s.roundSeen < len(s.lanes) {
		return
	}
	noOutput := !s.roundOutput
	s.roundOpen = false
	s.roundSeen = 0
	s.roundOutput = false
	if noOutput && s.queuedItems > 0 {
		s.fastForwardRoundLocked()
	}
}

func (s *Scheduler) fastForwardRoundLocked() {
	rounds := int64(math.MaxInt64)
	for i := range s.lanes {
		l := &s.lanes[i]
		if len(l.queue) == 0 {
			continue
		}
		need := scheduleCost(l.queue[0]) - l.deficit
		if need <= 0 {
			rounds = 0
			break
		}
		laneRounds := (need + l.weight - 1) / l.weight
		if laneRounds < rounds {
			rounds = laneRounds
		}
	}
	if rounds == int64(math.MaxInt64) || rounds <= 0 {
		return
	}
	for i := range s.lanes {
		if len(s.lanes[i].queue) > 0 {
			s.lanes[i].deficit += rounds * s.lanes[i].weight
		}
	}
	s.roundOpen = true
}

func queueBytes(item Item) int64 {
	if item.Bytes <= 0 {
		return 0
	}
	return int64(item.Bytes)
}

func scheduleCost(item Item) int64 {
	if item.Bytes <= 0 {
		return 1
	}
	return int64(item.Bytes)
}

func (s *Scheduler) drainLocked() []Item {
	if s.queuedItems == 0 {
		return nil
	}
	drained := make([]Item, 0, s.queuedItems)
	for i := range s.lanes {
		drained = append(drained, s.lanes[i].queue...)
		for j := range s.lanes[i].queue {
			s.lanes[i].queue[j] = Item{}
		}
		s.lanes[i].queue = nil
		s.lanes[i].deficit = 0
	}
	s.queuedItems = 0
	s.queuedBytes = 0
	return drained
}
