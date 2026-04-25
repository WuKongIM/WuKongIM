package multiraft

import (
	"testing"
	"time"
)

func TestSchedulerRequeueAfterDirtyDoneDeliversSlot(t *testing.T) {
	s := newScheduler()
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
	s := newScheduler()
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
