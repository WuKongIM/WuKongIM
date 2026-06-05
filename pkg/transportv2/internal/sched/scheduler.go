// Package sched provides byte-aware priority scheduling for transport frames.
package sched

import (
	"context"
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

// Item is one schedulable transport frame plus optional release ownership.
type Item struct {
	// Priority selects the weighted scheduler lane.
	Priority core.Priority
	// Bytes is the queue and deficit cost for this item; negative values are treated as zero.
	Bytes int
	// Value carries the caller-owned frame payload or metadata.
	Value any
	// OnRelease releases item ownership when a caller decides not to write it.
	OnRelease func(error)
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
	queuedItems int
	queuedBytes int64
	stopped     bool
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
		return nil, core.ErrStopped
	}
	return s.nextBatchLocked(), nil
}

// Stop marks the scheduler stopped, drains queued items, and wakes all waiters.
func (s *Scheduler) Stop(err error) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		s.cond.Broadcast()
		return nil
	}
	s.stopped = true
	drained := s.drainLocked()
	s.cond.Broadcast()
	return drained
}

func (s *Scheduler) nextBatchLocked() []Item {
	var batch []Item
	var batchBytes int64

	for len(batch) < s.maxBatchFrames && s.queuedItems > 0 {
		added := false
		checked := 0
		for len(batch) < s.maxBatchFrames && s.queuedItems > 0 {
			idx := s.nextLane
			s.nextLane = (s.nextLane + 1) % len(s.lanes)
			checked++

			l := &s.lanes[idx]
			l.deficit += l.weight
			if len(l.queue) == 0 {
				if checked >= len(s.lanes) {
					if len(batch) > 0 {
						break
					}
					checked = 0
				}
				continue
			}

			item := l.queue[0]
			itemBytes := int64(item.Bytes)
			if itemBytes > l.deficit || batchBytes+itemBytes > s.maxBatchBytes {
				if checked >= len(s.lanes) {
					if len(batch) > 0 {
						break
					}
					checked = 0
				}
				continue
			}

			l.queue[0] = Item{}
			l.queue = l.queue[1:]
			l.deficit -= itemBytes
			s.queuedItems--
			s.queuedBytes -= itemBytes
			batch = append(batch, item)
			batchBytes += itemBytes
			added = true
			break
		}
		if !added {
			break
		}
	}

	return batch
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
