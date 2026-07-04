// Package sched provides byte-aware priority scheduling for transport frames.
package sched

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
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
	// Observer receives scheduler pressure events; nil disables observation callbacks.
	Observer core.Observer
	// SourceID identifies the owning connection for aggregate metrics; it is not exported as a metric label.
	SourceID uint64
}

// Item is one schedulable transport frame.
type Item struct {
	// Priority selects the weighted scheduler lane.
	Priority core.Priority
	// Bytes is the queue and batch byte size; negative values are treated as zero.
	Bytes int
	// Value carries the caller-owned frame payload or metadata.
	Value any
	// enqueuedAt records when the item entered the queue for wait observations.
	enqueuedAt time.Time
}

type lane struct {
	priority core.Priority
	weight   int64
	deficit  int64
	queue    []Item
	head     int
	items    int
	bytes    int64
}

// Scheduler is a byte-aware weighted priority queue.
type Scheduler struct {
	mu   sync.Mutex
	cond *sync.Cond

	maxItems       int
	maxBytes       int64
	maxBatchFrames int
	maxBatchBytes  int64
	observer       core.Observer
	sourceID       uint64

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
		observer:       cfg.Observer,
		sourceID:       cfg.SourceID,
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
		total, lane := s.snapshotQueuePair(item.Priority)
		s.observeEnqueue("canceled", item, total, lane)
		return err
	}
	if err := item.Priority.Validate(); err != nil {
		total, lane := s.snapshotQueuePair(item.Priority)
		s.observeEnqueue("invalid", item, total, lane)
		return err
	}
	if item.Bytes < 0 {
		item.Bytes = 0
	}

	s.mu.Lock()

	if s.stopped {
		snapshot := s.snapshotQueueLocked()
		laneSnapshot := s.snapshotLaneQueueLocked(item.Priority)
		s.mu.Unlock()
		s.observeEnqueue("stopped", item, snapshot, laneSnapshot)
		return core.ErrStopped
	}
	if err := ctx.Err(); err != nil {
		snapshot := s.snapshotQueueLocked()
		laneSnapshot := s.snapshotLaneQueueLocked(item.Priority)
		s.mu.Unlock()
		s.observeEnqueue("canceled", item, snapshot, laneSnapshot)
		return err
	}
	if s.maxItems > 0 && s.queuedItems+1 > s.maxItems {
		snapshot := s.snapshotQueueLocked()
		laneSnapshot := s.snapshotLaneQueueLocked(item.Priority)
		s.mu.Unlock()
		s.observeEnqueue("full", item, snapshot, laneSnapshot)
		return core.ErrQueueFull
	}
	if s.maxBytes > 0 && s.queuedBytes+int64(item.Bytes) > s.maxBytes {
		snapshot := s.snapshotQueueLocked()
		laneSnapshot := s.snapshotLaneQueueLocked(item.Priority)
		s.mu.Unlock()
		s.observeEnqueue("full", item, snapshot, laneSnapshot)
		return core.ErrQueueFull
	}

	for i := range s.lanes {
		if s.lanes[i].priority == item.Priority {
			s.lanes[i].compactQueueIfFull()
			item.enqueuedAt = time.Now()
			s.lanes[i].queue = append(s.lanes[i].queue, item)
			s.lanes[i].items++
			s.lanes[i].bytes += queueBytes(item)
			s.queuedItems++
			s.queuedBytes += int64(item.Bytes)
			snapshot := s.snapshotQueueLocked()
			laneSnapshot := s.snapshotLaneQueueLocked(item.Priority)
			s.cond.Signal()
			s.mu.Unlock()
			s.observeEnqueue("ok", item, snapshot, laneSnapshot)
			return nil
		}
	}
	snapshot := s.snapshotQueueLocked()
	laneSnapshot := s.snapshotLaneQueueLocked(item.Priority)
	s.mu.Unlock()
	s.observeEnqueue("invalid", item, snapshot, laneSnapshot)
	return core.ErrInvalidPriority
}

// NextBatch returns the next weighted batch without blocking.
func (s *Scheduler) NextBatch() []Item {
	s.mu.Lock()

	if s.queuedItems == 0 {
		s.mu.Unlock()
		return nil
	}
	batch, events := s.nextBatchLocked(nil, s.observer != nil)
	s.mu.Unlock()
	s.observeAll(events)
	return batch
}

// WaitBatch blocks until a batch is available or the scheduler is stopped.
func (s *Scheduler) WaitBatch() ([]Item, error) {
	s.mu.Lock()

	for s.queuedItems == 0 && !s.stopped {
		s.cond.Wait()
	}
	if s.stopped {
		s.mu.Unlock()
		return nil, s.stopErr
	}
	batch, events := s.nextBatchLocked(nil, s.observer != nil)
	s.mu.Unlock()
	s.observeAll(events)
	return batch, nil
}

// WaitBatchInto blocks until a batch is available or the scheduler is stopped.
//
// It appends the returned items into dst[:0], allowing hot callers to reuse
// batch storage across write-loop iterations.
func (s *Scheduler) WaitBatchInto(dst []Item) ([]Item, error) {
	s.mu.Lock()

	for s.queuedItems == 0 && !s.stopped {
		s.cond.Wait()
	}
	if s.stopped {
		s.mu.Unlock()
		return nil, s.stopErr
	}
	batch, events := s.nextBatchLocked(dst, s.observer != nil)
	s.mu.Unlock()
	s.observeAll(events)
	return batch, nil
}

// Stop marks the scheduler stopped, drains queued items back to caller ownership, and wakes all waiters.
func (s *Scheduler) Stop(err error) []Item {
	s.mu.Lock()

	if s.stopped {
		s.cond.Broadcast()
		s.mu.Unlock()
		return nil
	}
	if err == nil {
		err = core.ErrStopped
	}
	s.stopped = true
	s.stopErr = err
	drained := s.drainLocked()
	var events []core.Event
	if s.observer != nil {
		events = s.stoppedQueueEventsLocked()
	}
	s.cond.Broadcast()
	s.mu.Unlock()

	s.observeAll(events)
	return drained
}

func (s *Scheduler) nextBatchLocked(dst []Item, observe bool) ([]Item, []core.Event) {
	batch := dst[:0]
	if batch == nil {
		batchCap := s.maxBatchFrames
		if s.queuedItems < batchCap {
			batchCap = s.queuedItems
		}
		batch = make([]Item, 0, batchCap)
	}
	var events []core.Event
	if observe {
		events = make([]core.Event, 0, cap(batch)+len(s.lanes))
	}
	var batchBytes int64
	var touchedMask uint8

	for len(batch) < s.maxBatchFrames && s.queuedItems > 0 {
		s.openRoundLocked()

		laneIndex := s.nextLane
		l := &s.lanes[s.nextLane]
		if l.items == 0 {
			s.finishLaneLocked()
			continue
		}

		item := l.front()
		itemCost := scheduleCost(item)
		itemBytes := queueBytes(item)
		if itemCost > l.deficit {
			s.finishLaneLocked()
			continue
		}
		if batchBytes+itemBytes > s.maxBatchBytes && len(batch) > 0 {
			break
		}

		l.popFront()
		l.items--
		l.bytes -= itemBytes
		l.deficit -= itemCost
		s.queuedItems--
		s.queuedBytes -= itemBytes
		batch = append(batch, item)
		if observe {
			events = append(events, s.waitEventLocked(item, itemBytes))
			touchedMask |= 1 << uint(laneIndex)
		}
		batchBytes += itemBytes
		s.roundOutput = true

		if len(batch) >= s.maxBatchFrames || batchBytes >= s.maxBatchBytes {
			break
		}
		if l.items == 0 {
			s.finishLaneLocked()
		}
	}

	if observe {
		for i := range s.lanes {
			if touchedMask&(1<<uint(i)) == 0 {
				continue
			}
			events = append(events, s.queueEventLocked(s.lanes[i].priority, "ok"))
		}
	}

	return batch, events
}

func (s *Scheduler) openRoundLocked() {
	if s.roundOpen {
		return
	}
	for i := range s.lanes {
		if s.lanes[i].items > 0 {
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
		if l.items == 0 {
			continue
		}
		need := scheduleCost(l.front()) - l.deficit
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
		if s.lanes[i].items > 0 {
			s.lanes[i].deficit += rounds * s.lanes[i].weight
		}
	}
	s.roundOpen = true
}

func (l *lane) front() Item {
	return l.queue[l.head]
}

func (l *lane) popFront() {
	l.queue[l.head] = Item{}
	l.head++
	if l.head == len(l.queue) {
		l.queue = l.queue[:0]
		l.head = 0
	}
}

func (l *lane) compactQueueIfFull() {
	if l.items == 0 {
		l.queue = l.queue[:0]
		l.head = 0
		return
	}
	if l.head == 0 || len(l.queue) < cap(l.queue) {
		return
	}
	copy(l.queue, l.queue[l.head:])
	for i := l.items; i < len(l.queue); i++ {
		l.queue[i] = Item{}
	}
	l.queue = l.queue[:l.items]
	l.head = 0
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

type queueSnapshot struct {
	items         int
	capacity      int
	bytes         int64
	bytesCapacity int64
}

func (s *Scheduler) snapshotQueue() queueSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotQueueLocked()
}

func (s *Scheduler) snapshotQueuePair(priority core.Priority) (queueSnapshot, queueSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotQueueLocked(), s.snapshotLaneQueueLocked(priority)
}

func (s *Scheduler) snapshotQueueLocked() queueSnapshot {
	return queueSnapshot{
		items:         s.queuedItems,
		capacity:      s.maxItems,
		bytes:         s.queuedBytes,
		bytesCapacity: s.maxBytes,
	}
}

func (s *Scheduler) snapshotLaneQueueLocked(priority core.Priority) queueSnapshot {
	snapshot := queueSnapshot{
		capacity:      s.maxItems,
		bytesCapacity: s.maxBytes,
	}
	for i := range s.lanes {
		if s.lanes[i].priority != priority {
			continue
		}
		snapshot.items = s.lanes[i].items
		snapshot.bytes = s.lanes[i].bytes
		return snapshot
	}
	return snapshot
}

func (s *Scheduler) waitEventLocked(item Item, itemBytes int64) core.Event {
	duration := time.Duration(0)
	if !item.enqueuedAt.IsZero() {
		duration = time.Since(item.enqueuedAt)
		if duration < 0 {
			duration = 0
		}
	}
	return core.Event{
		Name:          "scheduler_wait",
		SourceID:      s.sourceID,
		Priority:      item.Priority,
		Result:        "ok",
		Items:         s.queuedItems,
		Capacity:      s.maxItems,
		Bytes:         int(itemBytes),
		BytesCapacity: s.maxBytes,
		Duration:      duration,
	}
}

func (s *Scheduler) queueEventLocked(priority core.Priority, result string) core.Event {
	snapshot := s.snapshotLaneQueueLocked(priority)
	return core.Event{
		Name:          "scheduler_queue",
		SourceID:      s.sourceID,
		Priority:      priority,
		Result:        result,
		Items:         snapshot.items,
		Capacity:      snapshot.capacity,
		Bytes:         int(snapshot.bytes),
		BytesCapacity: snapshot.bytesCapacity,
	}
}

func (s *Scheduler) observeEnqueue(result string, item Item, snapshot queueSnapshot, laneSnapshot queueSnapshot) {
	if s.observer == nil {
		return
	}
	s.observer.ObserveTransport(core.Event{
		Name:          "scheduler_admission",
		SourceID:      s.sourceID,
		Priority:      item.Priority,
		Result:        result,
		Items:         snapshot.items,
		Capacity:      snapshot.capacity,
		Bytes:         item.Bytes,
		BytesCapacity: snapshot.bytesCapacity,
	})
	s.observer.ObserveTransport(core.Event{
		Name:          "scheduler_queue",
		SourceID:      s.sourceID,
		Priority:      item.Priority,
		Result:        result,
		Items:         laneSnapshot.items,
		Capacity:      laneSnapshot.capacity,
		Bytes:         int(laneSnapshot.bytes),
		BytesCapacity: laneSnapshot.bytesCapacity,
	})
}

func (s *Scheduler) stoppedQueueEventsLocked() []core.Event {
	events := make([]core.Event, 0, len(s.lanes))
	for _, lane := range s.lanes {
		events = append(events, core.Event{
			Name:          "scheduler_queue",
			SourceID:      s.sourceID,
			Priority:      lane.priority,
			Result:        "stopped",
			Capacity:      s.maxItems,
			BytesCapacity: s.maxBytes,
		})
	}
	return events
}

func (s *Scheduler) observeAll(events []core.Event) {
	if s.observer == nil {
		return
	}
	for _, event := range events {
		s.observer.ObserveTransport(event)
	}
}

func (s *Scheduler) drainLocked() []Item {
	if s.queuedItems == 0 {
		return nil
	}
	drained := make([]Item, 0, s.queuedItems)
	for i := range s.lanes {
		lane := &s.lanes[i]
		drained = append(drained, lane.queue[lane.head:]...)
		for j := range lane.queue {
			lane.queue[j] = Item{}
		}
		lane.queue = nil
		lane.head = 0
		lane.deficit = 0
		lane.items = 0
		lane.bytes = 0
	}
	s.queuedItems = 0
	s.queuedBytes = 0
	return drained
}
