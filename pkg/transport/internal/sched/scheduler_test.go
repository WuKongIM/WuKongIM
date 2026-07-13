package sched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

type recordingObserver struct {
	mu     sync.Mutex
	events []core.Event
}

type allocationObserver struct {
	events int
}

func (o *allocationObserver) ObserveTransport(core.Event) {
	o.events++
}

func (o *recordingObserver) ObserveTransport(event core.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingObserver) snapshot() []core.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]core.Event(nil), o.events...)
}

func TestSchedulerObservesQueueAdmissionAndWait(t *testing.T) {
	observer := &recordingObserver{}
	s := New(Config{
		MaxItems:       1,
		MaxBytes:       10,
		MaxBatchFrames: 1,
		MaxBatchBytes:  10,
		Observer:       observer,
		SourceID:       77,
	})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    3,
		Value:    "first",
	}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}
	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    3,
		Value:    "second",
	}); !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("Enqueue(second) error = %v, want ErrQueueFull", err)
	}

	batch := s.NextBatch()
	if len(batch) != 1 || batch[0].Value != "first" {
		t.Fatalf("NextBatch() = %#v, want first item", batch)
	}

	events := observer.snapshot()
	okAdmission := findEvent(events, "scheduler_admission", "ok")
	if okAdmission == nil {
		t.Fatalf("missing scheduler_admission ok event: %#v", events)
	}
	if okAdmission.SourceID != 77 || okAdmission.Priority != core.PriorityRPC || okAdmission.Items != 1 || okAdmission.Capacity != 1 ||
		okAdmission.Bytes != 3 || okAdmission.BytesCapacity != 10 {
		t.Fatalf("scheduler_admission ok = %+v, want priority/items/capacity/bytes populated", *okAdmission)
	}

	fullAdmission := findEvent(events, "scheduler_admission", "full")
	if fullAdmission == nil {
		t.Fatalf("missing scheduler_admission full event: %#v", events)
	}
	if fullAdmission.SourceID != 77 || fullAdmission.Priority != core.PriorityRPC || fullAdmission.Items != 1 || fullAdmission.Capacity != 1 ||
		fullAdmission.Bytes != 3 || fullAdmission.BytesCapacity != 10 {
		t.Fatalf("scheduler_admission full = %+v, want bounded full snapshot", *fullAdmission)
	}

	queueEvent := findEvent(events, "scheduler_queue", "ok")
	if queueEvent == nil {
		t.Fatalf("missing scheduler_queue ok event: %#v", events)
	}
	if queueEvent.SourceID != 77 || queueEvent.Priority != core.PriorityRPC || queueEvent.Items != 1 || queueEvent.Capacity != 1 ||
		queueEvent.Bytes != 3 || queueEvent.BytesCapacity != 10 {
		t.Fatalf("scheduler_queue ok = %+v, want queue state populated", *queueEvent)
	}
	drainedQueueEvent := findLastEventByPriority(events, "scheduler_queue", "ok", core.PriorityRPC)
	if drainedQueueEvent == nil {
		t.Fatalf("missing drained scheduler_queue ok event: %#v", events)
	}
	if drainedQueueEvent.SourceID != 77 || drainedQueueEvent.Items != 0 ||
		drainedQueueEvent.Capacity != 1 || drainedQueueEvent.Bytes != 0 ||
		drainedQueueEvent.BytesCapacity != 10 {
		t.Fatalf("drained scheduler_queue ok = %+v, want source-scoped drained queue", *drainedQueueEvent)
	}

	waitEvent := findEvent(events, "scheduler_wait", "ok")
	if waitEvent == nil {
		t.Fatalf("missing scheduler_wait ok event: %#v", events)
	}
	if waitEvent.SourceID != 77 || waitEvent.Priority != core.PriorityRPC || waitEvent.Bytes != 3 || waitEvent.Duration < 0 {
		t.Fatalf("scheduler_wait = %+v, want priority/bytes and non-negative duration", *waitEvent)
	}
}

func TestSchedulerStopObservesSourceQueueClear(t *testing.T) {
	observer := &recordingObserver{}
	s := New(Config{
		MaxItems:      4,
		MaxBytes:      20,
		MaxBatchBytes: 20,
		Observer:      observer,
		SourceID:      77,
	})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    3,
		Value:    "queued",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	drained := s.Stop(nil)
	if len(drained) != 1 {
		t.Fatalf("Stop() drained %d items, want 1", len(drained))
	}

	events := observer.snapshot()
	stoppedQueue := findEventByPriority(events, "scheduler_queue", "stopped", core.PriorityRPC)
	if stoppedQueue == nil {
		t.Fatalf("missing scheduler_queue stopped event: %#v", events)
	}
	if stoppedQueue.SourceID != 77 || stoppedQueue.Priority != core.PriorityRPC ||
		stoppedQueue.Items != 0 || stoppedQueue.Capacity != 4 ||
		stoppedQueue.Bytes != 0 || stoppedQueue.BytesCapacity != 20 {
		t.Fatalf("scheduler_queue stopped = %+v, want source-scoped zero queue", *stoppedQueue)
	}
}

func TestSchedulerQueueEventsUsePriorityLaneDepth(t *testing.T) {
	observer := &recordingObserver{}
	s := New(Config{
		MaxItems:       4,
		MaxBytes:       20,
		MaxBatchFrames: 1,
		MaxBatchBytes:  20,
		Observer:       observer,
		SourceID:       77,
	})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    3,
		Value:    "rpc",
	}); err != nil {
		t.Fatalf("Enqueue(rpc) error = %v", err)
	}
	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityBulk,
		Bytes:    5,
		Value:    "bulk",
	}); err != nil {
		t.Fatalf("Enqueue(bulk) error = %v", err)
	}
	batch := s.NextBatch()
	if len(batch) != 1 || batch[0].Value != "rpc" {
		t.Fatalf("NextBatch() = %#v, want rpc first", batch)
	}

	events := observer.snapshot()
	rpcQueue := findLastEventByPriority(events, "scheduler_queue", "ok", core.PriorityRPC)
	if rpcQueue == nil {
		t.Fatalf("missing rpc scheduler_queue event: %#v", events)
	}
	if rpcQueue.Items != 0 || rpcQueue.Bytes != 0 || rpcQueue.Capacity != 4 || rpcQueue.BytesCapacity != 20 {
		t.Fatalf("rpc scheduler_queue = %+v, want drained rpc lane only", *rpcQueue)
	}
	bulkQueue := findLastEventByPriority(events, "scheduler_queue", "ok", core.PriorityBulk)
	if bulkQueue == nil {
		t.Fatalf("missing bulk scheduler_queue event: %#v", events)
	}
	if bulkQueue.Items != 1 || bulkQueue.Bytes != 5 || bulkQueue.Capacity != 4 || bulkQueue.BytesCapacity != 20 {
		t.Fatalf("bulk scheduler_queue = %+v, want queued bulk lane only", *bulkQueue)
	}
}

func TestSchedulerBatchWaitObservationsPreserveQueueDepth(t *testing.T) {
	observer := &recordingObserver{}
	s := New(Config{
		MaxItems:       8,
		MaxBytes:       1024,
		MaxBatchFrames: 3,
		MaxBatchBytes:  1024,
		Observer:       observer,
	})
	for i := 0; i < 4; i++ {
		if err := s.Enqueue(context.Background(), Item{
			Priority: core.PriorityRPC,
			Bytes:    i + 1,
			Value:    i,
		}); err != nil {
			t.Fatalf("Enqueue(%d) error = %v", i, err)
		}
	}

	batch := s.NextBatch()
	if len(batch) != 3 {
		t.Fatalf("NextBatch() len = %d, want 3", len(batch))
	}

	events := observer.snapshot()
	var waits []core.Event
	for _, event := range events {
		if event.Name == "scheduler_wait" {
			waits = append(waits, event)
		}
	}
	if len(waits) != 3 {
		t.Fatalf("scheduler_wait event count = %d, want 3", len(waits))
	}
	for i, event := range waits {
		if want := 3 - i; event.Items != want {
			t.Fatalf("scheduler_wait[%d].Items = %d, want %d", i, event.Items, want)
		}
		if want := i + 1; event.Bytes != want {
			t.Fatalf("scheduler_wait[%d].Bytes = %d, want %d", i, event.Bytes, want)
		}
	}
	drainedQueueEvent := findLastEventByPriority(events, "scheduler_queue", "ok", core.PriorityRPC)
	if drainedQueueEvent == nil || drainedQueueEvent.Items != 1 || drainedQueueEvent.Bytes != 4 {
		t.Fatalf("post-batch scheduler_queue = %+v, want one queued item with four bytes", drainedQueueEvent)
	}
}

func findEvent(events []core.Event, name, result string) *core.Event {
	for i := range events {
		if events[i].Name == name && events[i].Result == result {
			return &events[i]
		}
	}
	return nil
}

func findEventByPriority(events []core.Event, name, result string, priority core.Priority) *core.Event {
	for i := range events {
		if events[i].Name == name && events[i].Result == result && events[i].Priority == priority {
			return &events[i]
		}
	}
	return nil
}

func findLastEventByPriority(events []core.Event, name, result string, priority core.Priority) *core.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Name == name && events[i].Result == result && events[i].Priority == priority {
			return &events[i]
		}
	}
	return nil
}

func TestLaneCountersStayConsistent(t *testing.T) {
	s := New(Config{
		MaxItems:       64,
		MaxBytes:       4096,
		MaxBatchFrames: 4,
		MaxBatchBytes:  256,
	})
	priorities := []core.Priority{
		core.PriorityRaft, core.PriorityControl, core.PriorityRPC, core.PriorityBulk,
	}
	for round := 0; round < 50; round++ {
		for i, p := range priorities {
			_ = s.Enqueue(context.Background(), Item{Priority: p, Bytes: (i + 1) * 7, Value: round})
		}
		_ = s.NextBatch()
		s.mu.Lock()
		for li := range s.lanes {
			l := &s.lanes[li]
			wantItems := len(l.queue[l.head:])
			var wantBytes int64
			for _, it := range l.queue[l.head:] {
				wantBytes += queueBytes(it)
			}
			if l.items != wantItems {
				s.mu.Unlock()
				t.Fatalf("lane %d items = %d, want %d", li, l.items, wantItems)
			}
			if l.bytes != wantBytes {
				s.mu.Unlock()
				t.Fatalf("lane %d bytes = %d, want %d", li, l.bytes, wantBytes)
			}
		}
		s.mu.Unlock()
	}
}

func TestNextBatchWithoutObserverAvoidsEventAllocations(t *testing.T) {
	s := New(Config{
		MaxItems:       1,
		MaxBytes:       1024,
		MaxBatchFrames: 1,
		MaxBatchBytes:  1024,
	})
	item := Item{Priority: core.PriorityRPC, Bytes: 128, Value: 1}
	allocs := testing.AllocsPerRun(1000, func() {
		if err := s.Enqueue(context.Background(), item); err != nil {
			t.Fatalf("Enqueue() error = %v", err)
		}
		batch := s.NextBatch()
		if len(batch) != 1 {
			t.Fatalf("NextBatch() len = %d, want 1", len(batch))
		}
	})
	if allocs > 1 {
		t.Fatalf("allocs per Enqueue+NextBatch = %.2f, want <= 1", allocs)
	}
}

func TestWaitBatchIntoReusesCallerStorage(t *testing.T) {
	s := New(Config{
		MaxItems:       1,
		MaxBytes:       1024,
		MaxBatchFrames: 1,
		MaxBatchBytes:  1024,
	})
	item := Item{Priority: core.PriorityRPC, Bytes: 128, Value: 1}
	dst := make([]Item, 0, 1)

	if err := s.Enqueue(context.Background(), item); err != nil {
		t.Fatalf("warm-up Enqueue() error = %v", err)
	}
	batch, err := s.WaitBatchInto(dst)
	if err != nil {
		t.Fatalf("warm-up WaitBatchInto() error = %v", err)
	}
	if len(batch) != 1 {
		t.Fatalf("warm-up WaitBatchInto() len = %d, want 1", len(batch))
	}
	dst = batch[:0]

	allocs := testing.AllocsPerRun(1000, func() {
		if err := s.Enqueue(context.Background(), item); err != nil {
			t.Fatalf("Enqueue() error = %v", err)
		}
		batch, err := s.WaitBatchInto(dst)
		if err != nil {
			t.Fatalf("WaitBatchInto() error = %v", err)
		}
		if len(batch) != 1 {
			t.Fatalf("WaitBatchInto() len = %d, want 1", len(batch))
		}
		dst = batch[:0]
	})
	if allocs != 0 {
		t.Fatalf("allocs per Enqueue+WaitBatchInto = %.2f, want 0", allocs)
	}
}

func TestWaitBatchIntoWithObserverAvoidsEventAllocations(t *testing.T) {
	observer := &allocationObserver{}
	s := New(Config{
		MaxItems:       1,
		MaxBytes:       1024,
		MaxBatchFrames: 1,
		MaxBatchBytes:  1024,
		Observer:       observer,
	})
	item := Item{Priority: core.PriorityRPC, Bytes: 128, Value: 1}
	dst := make([]Item, 0, 1)

	if err := s.Enqueue(context.Background(), item); err != nil {
		t.Fatalf("warm-up Enqueue() error = %v", err)
	}
	batch, err := s.WaitBatchInto(dst)
	if err != nil {
		t.Fatalf("warm-up WaitBatchInto() error = %v", err)
	}
	if len(batch) != 1 {
		t.Fatalf("warm-up WaitBatchInto() len = %d, want 1", len(batch))
	}
	dst = batch[:0]

	allocs := testing.AllocsPerRun(1000, func() {
		if err := s.Enqueue(context.Background(), item); err != nil {
			t.Fatalf("Enqueue() error = %v", err)
		}
		batch, err := s.WaitBatchInto(dst)
		if err != nil {
			t.Fatalf("WaitBatchInto() error = %v", err)
		}
		if len(batch) != 1 {
			t.Fatalf("WaitBatchInto() len = %d, want 1", len(batch))
		}
		dst = batch[:0]
	})
	if allocs != 0 {
		t.Fatalf("allocs per observed Enqueue+WaitBatchInto = %.2f, want 0", allocs)
	}
	if observer.events == 0 {
		t.Fatal("observer did not receive scheduler events")
	}
}

func TestWaitBatchIntoPreservesLaneQueueStorage(t *testing.T) {
	const batchSize = 64
	s := New(Config{
		MaxItems:       batchSize,
		MaxBytes:       batchSize,
		MaxBatchFrames: batchSize,
		MaxBatchBytes:  batchSize,
	})
	items := make([]Item, batchSize)
	for i := range items {
		items[i] = Item{Priority: core.PriorityRaft, Bytes: 1, Value: i}
	}
	dst := make([]Item, 0, batchSize)

	for _, item := range items {
		if err := s.Enqueue(context.Background(), item); err != nil {
			t.Fatalf("warm-up Enqueue() error = %v", err)
		}
	}
	batch, err := s.WaitBatchInto(dst)
	if err != nil {
		t.Fatalf("warm-up WaitBatchInto() error = %v", err)
	}
	if len(batch) != batchSize {
		t.Fatalf("warm-up WaitBatchInto() len = %d, want %d", len(batch), batchSize)
	}
	dst = batch[:0]

	allocs := testing.AllocsPerRun(1000, func() {
		for _, item := range items {
			if err := s.Enqueue(context.Background(), item); err != nil {
				t.Fatalf("Enqueue() error = %v", err)
			}
		}
		batch, err := s.WaitBatchInto(dst)
		if err != nil {
			t.Fatalf("WaitBatchInto() error = %v", err)
		}
		if len(batch) != batchSize {
			t.Fatalf("WaitBatchInto() len = %d, want %d", len(batch), batchSize)
		}
		dst = batch[:0]
	})
	if allocs != 0 {
		t.Fatalf("allocs per batched Enqueue+WaitBatchInto = %.2f, want 0", allocs)
	}
}

func TestNextBatchIntoReusesCallerStorage(t *testing.T) {
	s := New(Config{
		MaxBatchFrames: 4,
		MaxBatchBytes:  1024,
	})
	for i := 0; i < 3; i++ {
		if err := s.Enqueue(context.Background(), Item{
			Priority: core.PriorityRPC,
			Bytes:    1,
			Value:    i,
		}); err != nil {
			t.Fatalf("Enqueue(%d) error = %v", i, err)
		}
	}

	dst := make([]Item, 0, 4)
	storage := &dst[:cap(dst)][0]
	batch := s.NextBatchInto(dst)
	if len(batch) != 3 {
		t.Fatalf("NextBatchInto() len = %d, want 3", len(batch))
	}
	if &batch[:cap(batch)][0] != storage {
		t.Fatal("NextBatchInto() did not reuse caller storage")
	}
	if next := s.NextBatchInto(batch[:0]); len(next) != 0 {
		t.Fatalf("NextBatchInto() after drain len = %d, want 0", len(next))
	}
}

func TestConcurrentEnqueueAccountsAllItems(t *testing.T) {
	observer := &recordingObserver{}
	s := New(Config{
		MaxItems:       100000,
		MaxBytes:       1 << 30,
		MaxBatchFrames: 64,
		MaxBatchBytes:  1 << 20,
		Observer:       observer,
		SourceID:       5,
	})

	const goroutines, per = 16, 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if err := s.Enqueue(context.Background(), Item{
					Priority: core.PriorityRaft, Bytes: 1, Value: i,
				}); err != nil {
					t.Errorf("Enqueue() error = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	s.mu.Lock()
	gotItems := s.queuedItems
	s.mu.Unlock()
	if gotItems != goroutines*per {
		t.Fatalf("queuedItems = %d, want %d", gotItems, goroutines*per)
	}

	okCount := 0
	for _, e := range observer.snapshot() {
		if e.Name == "scheduler_admission" && e.Result == "ok" {
			okCount++
		}
	}
	if okCount != goroutines*per {
		t.Fatalf("scheduler_admission ok count = %d, want %d", okCount, goroutines*per)
	}
}

func TestEnqueueRejectsInvalidPriority(t *testing.T) {
	s := New(Config{})

	err := s.Enqueue(context.Background(), Item{
		Priority: core.Priority(99),
		Bytes:    1,
		Value:    "bad",
	})

	if !errors.Is(err, core.ErrInvalidPriority) {
		t.Fatalf("Enqueue() error = %v, want ErrInvalidPriority", err)
	}
}

func TestEnqueueEnforcesMaxBytes(t *testing.T) {
	s := New(Config{MaxBytes: 10})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    7,
		Value:    "first",
	}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}

	err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    4,
		Value:    "second",
	})

	if !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("Enqueue(second) error = %v, want ErrQueueFull", err)
	}
}

func TestEnqueueEnforcesMaxItems(t *testing.T) {
	s := New(Config{MaxItems: 1})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    1,
		Value:    "first",
	}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}

	err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    1,
		Value:    "second",
	})

	if !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("Enqueue(second) error = %v, want ErrQueueFull", err)
	}
}

func TestNextBatchReturnsOversizedItemAsSingleton(t *testing.T) {
	s := New(Config{MaxBatchBytes: 8})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    9,
		Value:    "oversized",
	}); err != nil {
		t.Fatalf("Enqueue(oversized) error = %v", err)
	}
	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    1,
		Value:    "small",
	}); err != nil {
		t.Fatalf("Enqueue(small) error = %v", err)
	}

	batch := s.NextBatch()
	if len(batch) != 1 {
		t.Fatalf("NextBatch() len = %d, want 1", len(batch))
	}
	if batch[0].Value != "oversized" {
		t.Fatalf("NextBatch()[0].Value = %v, want oversized", batch[0].Value)
	}
}

func TestNextBatchHonorsMaxBatchBytes(t *testing.T) {
	s := New(Config{
		MaxBatchFrames: 4,
		MaxBatchBytes:  8,
	})

	for _, item := range []Item{
		{Priority: core.PriorityRaft, Bytes: 5, Value: "first"},
		{Priority: core.PriorityRaft, Bytes: 4, Value: "second"},
	} {
		if err := s.Enqueue(context.Background(), item); err != nil {
			t.Fatalf("Enqueue(%v) error = %v", item.Value, err)
		}
	}

	batch := s.NextBatch()
	if len(batch) != 1 {
		t.Fatalf("NextBatch() len = %d, want 1", len(batch))
	}
	if batch[0].Value != "first" {
		t.Fatalf("NextBatch()[0].Value = %v, want first", batch[0].Value)
	}
	if batch[0].Bytes > 8 {
		t.Fatalf("NextBatch() bytes = %d, want <= 8", batch[0].Bytes)
	}
}

func TestNextBatchUsesByteWeightedLaneQuanta(t *testing.T) {
	s := New(Config{
		MaxBatchFrames: 19,
		MaxBatchBytes:  19,
	})

	priorities := []core.Priority{
		core.PriorityRaft,
		core.PriorityControl,
		core.PriorityRPC,
		core.PriorityBulk,
	}
	for _, priority := range priorities {
		for i := 0; i < 19; i++ {
			if err := s.Enqueue(context.Background(), Item{
				Priority: priority,
				Bytes:    1,
				Value:    priority,
			}); err != nil {
				t.Fatalf("Enqueue(%v, %d) error = %v", priority, i, err)
			}
		}
	}

	batch := s.NextBatch()
	if len(batch) != 19 {
		t.Fatalf("NextBatch() len = %d, want 19", len(batch))
	}

	counts := map[core.Priority]int{}
	for _, item := range batch {
		counts[item.Priority]++
	}
	want := map[core.Priority]int{
		core.PriorityRaft:    8,
		core.PriorityControl: 6,
		core.PriorityRPC:     4,
		core.PriorityBulk:    1,
	}
	for priority, wantCount := range want {
		if counts[priority] != wantCount {
			t.Fatalf("priority %v count = %d, want %d; batch=%#v", priority, counts[priority], wantCount, batch)
		}
	}
}

func TestNextBatchReturnsLargeFrameWithoutEmptySpin(t *testing.T) {
	s := New(Config{
		MaxBatchFrames: 1,
		MaxBatchBytes:  16,
	})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityBulk,
		Bytes:    16,
		Value:    "large",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	batch := s.NextBatch()
	if len(batch) != 1 || batch[0].Value != "large" {
		t.Fatalf("NextBatch() = %#v, want large frame", batch)
	}
}

func TestWeightedBatchEventuallyIncludesLowerPriority(t *testing.T) {
	s := New(Config{
		MaxBatchFrames: 1,
		MaxBatchBytes:  8,
	})

	for i := 0; i < 16; i++ {
		if err := s.Enqueue(context.Background(), Item{
			Priority: core.PriorityRaft,
			Bytes:    8,
			Value:    "raft",
		}); err != nil {
			t.Fatalf("Enqueue(raft %d) error = %v", i, err)
		}
	}
	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityBulk,
		Bytes:    8,
		Value:    "bulk",
	}); err != nil {
		t.Fatalf("Enqueue(bulk) error = %v", err)
	}

	for i := 0; i < 16; i++ {
		batch := s.NextBatch()
		if len(batch) != 1 {
			t.Fatalf("NextBatch(%d) len = %d, want 1", i, len(batch))
		}
		if batch[0].Priority == core.PriorityBulk {
			return
		}
	}

	t.Fatal("NextBatch() did not include bulk item within weighted turns")
}

func TestWaitBatchBlocksWhileEmptyAndWakesWhenEnqueued(t *testing.T) {
	s := New(Config{})
	got := make(chan []Item, 1)
	errc := make(chan error, 1)

	go func() {
		batch, err := s.WaitBatch()
		if err != nil {
			errc <- err
			return
		}
		got <- batch
	}()

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityControl,
		Bytes:    3,
		Value:    "wake",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case err := <-errc:
		t.Fatalf("WaitBatch() error = %v", err)
	case batch := <-got:
		if len(batch) != 1 || batch[0].Value != "wake" {
			t.Fatalf("WaitBatch() = %#v, want wake item", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitBatch did not wake after enqueue")
	}
}

func TestStopWakesWaitBatchAndReturnsQueuedItems(t *testing.T) {
	stopCause := errors.New("stop test")
	draining := New(Config{})
	if err := draining.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    5,
		Value:    "drained",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	drained := draining.Stop(stopCause)
	if len(drained) != 1 || drained[0].Value != "drained" {
		t.Fatalf("Stop() = %#v, want drained item", drained)
	}
	if _, err := draining.WaitBatch(); !errors.Is(err, stopCause) {
		t.Fatalf("WaitBatch(after custom stop) error = %v, want stop cause", err)
	}

	s := New(Config{})
	errc := make(chan error, 1)
	go func() {
		_, err := s.WaitBatch()
		errc <- err
	}()

	if drained := s.Stop(nil); len(drained) != 0 {
		t.Fatalf("Stop(empty) = %#v, want no drained items", drained)
	}

	select {
	case err := <-errc:
		if !errors.Is(err, core.ErrStopped) {
			t.Fatalf("WaitBatch() error = %v, want ErrStopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitBatch did not wake after Stop")
	}

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Value:    "after stop",
	}); !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Enqueue(after stop) error = %v, want ErrStopped", err)
	}

	if _, err := s.WaitBatch(); !errors.Is(err, core.ErrStopped) {
		t.Fatalf("WaitBatch(after stop) error = %v, want ErrStopped", err)
	}
}
