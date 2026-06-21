package multiraft

import (
	"testing"
	"time"
)

func TestSchedulerRequeueAfterDirtyDoneDeliversSlot(t *testing.T) {
	s := newScheduler(nil)
	slotID := SlotID(42)

	s.enqueue(slotID)
	select {
	case got := <-s.ch:
		if got != slotID {
			t.Fatalf("first dequeue = %d", got)
		}
	default:
		t.Fatal("slot was not enqueued")
	}

	s.begin(slotID)
	s.enqueue(slotID)
	if !s.done(slotID) {
		t.Fatal("done() = false, want true after dirty enqueue")
	}

	s.requeue(slotID)

	select {
	case got := <-s.ch:
		if got != slotID {
			t.Fatalf("requeued slot = %d", got)
		}
	default:
		t.Fatal("dirty slot was not delivered after requeue")
	}
}

func TestSchedulerEnqueueBuffersWhenChannelIsFull(t *testing.T) {
	s := newScheduler(nil)
	s.ch = make(chan SlotID, 1)

	s.enqueue(1)

	done := make(chan struct{})
	go func() {
		s.enqueue(2)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("enqueue() blocked while scheduler channel was full")
	}

	first := <-s.ch
	if first != 1 {
		t.Fatalf("first dequeue = %d", first)
	}

	s.begin(first)

	select {
	case got := <-s.ch:
		if got != 2 {
			t.Fatalf("second dequeue = %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("buffered slot was not dispatched after begin() freed capacity")
	}
}

func TestSchedulerPendingBufferReusesBackingAfterDrain(t *testing.T) {
	s := newScheduler(nil)
	s.ch = make(chan SlotID, 1)

	for i := 1; i <= 5; i++ {
		s.enqueue(SlotID(i))
	}

	s.mu.Lock()
	originalCap := cap(s.pending)
	pending := s.pendingLenLocked()
	s.mu.Unlock()
	if pending != 4 {
		t.Fatalf("pending len = %d, want 4", pending)
	}
	if originalCap == 0 {
		t.Fatal("pending cap = 0, want reusable backing array")
	}

	for i := 1; i <= 5; i++ {
		slotID := <-s.ch
		s.begin(slotID)
		_ = s.done(slotID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if got := s.pendingLenLocked(); got != 0 {
		t.Fatalf("pending len after drain = %d, want 0", got)
	}
	if cap(s.pending) < originalCap {
		t.Fatalf("pending cap after drain = %d, want at least %d", cap(s.pending), originalCap)
	}
}

func TestRuntimeTickScratchReusesBackingAndClearsReferences(t *testing.T) {
	rt := &Runtime{
		slots:     make(map[SlotID]*slot),
		scheduler: newScheduler(nil),
	}
	for i := 1; i <= 5; i++ {
		id := SlotID(i)
		rt.slots[id] = &slot{id: id}
	}

	rt.enqueueTickForOpenSlots()
	firstCap := cap(rt.tickSlots)
	if firstCap < len(rt.slots) {
		t.Fatalf("tick scratch cap = %d, want at least %d", firstCap, len(rt.slots))
	}
	if len(rt.tickSlots) != 0 {
		t.Fatalf("tick scratch len = %d, want 0", len(rt.tickSlots))
	}
	for i := 0; i < firstCap; i++ {
		if rt.tickSlots[:firstCap][i] != nil {
			t.Fatal("tick scratch retained slot reference")
		}
	}
	benchmarkDrainScheduler(rt.scheduler, len(rt.slots))

	rt.enqueueTickForOpenSlots()
	if cap(rt.tickSlots) != firstCap {
		t.Fatalf("tick scratch cap after reuse = %d, want %d", cap(rt.tickSlots), firstCap)
	}
}

func TestSchedulerReportsAdmissionAndState(t *testing.T) {
	obs := &recordingSlotSchedulerObserver{}
	s := newScheduler(obs)
	s.ch = make(chan SlotID, 1)

	s.enqueue(1)
	s.enqueue(1)
	s.begin(1)
	s.enqueue(1)
	requeue := s.done(1)
	if !requeue {
		t.Fatal("done() did not report dirty requeue")
	}
	s.requeue(1)

	if got := obs.admissions["ok"]; got != 1 {
		t.Fatalf("ok admissions = %d, want 1", got)
	}
	if got := obs.admissions["coalesced"]; got != 1 {
		t.Fatalf("coalesced admissions = %d, want 1", got)
	}
	if got := obs.admissions["dirty"]; got != 1 {
		t.Fatalf("dirty admissions = %d, want 1", got)
	}
	if len(obs.states) == 0 {
		t.Fatal("scheduler state was not observed")
	}
}

func TestSchedulerObserverCanReenterWithoutDeadlock(t *testing.T) {
	obs := &reentrantSlotSchedulerObserver{}
	s := newScheduler(obs)
	obs.scheduler = s

	done := make(chan struct{})
	go func() {
		s.enqueue(1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler observer reentry deadlocked")
	}
}

type recordingSlotSchedulerObserver struct {
	admissions map[string]int
	states     []SchedulerStateEvent
}

func (o *recordingSlotSchedulerObserver) ObserveSchedulerAdmission(result string) {
	if o.admissions == nil {
		o.admissions = make(map[string]int)
	}
	o.admissions[result]++
}

func (o *recordingSlotSchedulerObserver) SetSchedulerState(event SchedulerStateEvent) {
	o.states = append(o.states, event)
}

func (o *recordingSlotSchedulerObserver) SetSchedulerWorkers(int) {}

func (o *recordingSlotSchedulerObserver) SetSchedulerInflight(int) {}

func (o *recordingSlotSchedulerObserver) ObserveSchedulerTask(string, time.Duration) {}

type reentrantSlotSchedulerObserver struct {
	scheduler *scheduler
	once      bool
}

func (o *reentrantSlotSchedulerObserver) ObserveSchedulerAdmission(string) {
	if o.once || o.scheduler == nil {
		return
	}
	o.once = true
	o.scheduler.enqueue(2)
}

func (o *reentrantSlotSchedulerObserver) SetSchedulerState(SchedulerStateEvent) {}

func (o *reentrantSlotSchedulerObserver) SetSchedulerWorkers(int) {}

func (o *reentrantSlotSchedulerObserver) SetSchedulerInflight(int) {}

func (o *reentrantSlotSchedulerObserver) ObserveSchedulerTask(string, time.Duration) {}
