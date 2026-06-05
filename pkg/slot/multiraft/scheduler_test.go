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
