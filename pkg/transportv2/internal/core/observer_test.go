package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestObserverDrainDoesNotBlockWhenSinkIsSlow(t *testing.T) {
	sink := &blockingObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	drain := NewObserverDrain(sink)
	defer func() {
		close(sink.release)
		drain.Stop()
	}()

	drain.ObserveTransport(Event{Name: "first"})
	waitCoreClosed(t, sink.entered)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 4096; i++ {
			drain.ObserveTransport(Event{Name: "overflow"})
		}
		close(done)
	}()
	waitCoreClosed(t, done)
}

func TestObserverDrainPreservesTerminalCleanupEvents(t *testing.T) {
	sink := &recordingBlockingObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	drain := NewObserverDrain(sink)
	defer drain.Stop()

	drain.ObserveTransport(Event{Name: "first"})
	waitCoreClosed(t, sink.entered)
	for i := 0; i < defaultObserverQueueSize*2; i++ {
		drain.ObserveTransport(Event{Name: "overflow"})
	}

	terminalDone := make(chan struct{})
	go func() {
		drain.ObserveTransport(Event{Name: "scheduler_queue", SourceID: 99, Result: "stopped"})
		close(terminalDone)
	}()

	close(sink.release)
	waitCoreClosed(t, terminalDone)
	drain.Stop()

	if !sink.hasEvent(func(event Event) bool {
		return event.Name == "scheduler_queue" && event.SourceID == 99 && event.Result == "stopped"
	}) {
		t.Fatalf("terminal cleanup event was not delivered; events=%#v", sink.snapshot())
	}
}

type blockingObserver struct {
	enteredOnce atomic.Bool
	entered     chan struct{}
	release     chan struct{}
}

func (o *blockingObserver) ObserveTransport(Event) {
	if o.enteredOnce.CompareAndSwap(false, true) {
		close(o.entered)
	}
	<-o.release
}

type recordingBlockingObserver struct {
	mu          sync.Mutex
	enteredOnce atomic.Bool
	entered     chan struct{}
	release     chan struct{}
	events      []Event
}

func (o *recordingBlockingObserver) ObserveTransport(event Event) {
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
	if o.enteredOnce.CompareAndSwap(false, true) {
		close(o.entered)
	}
	<-o.release
}

func (o *recordingBlockingObserver) snapshot() []Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Event(nil), o.events...)
}

func (o *recordingBlockingObserver) hasEvent(predicate func(Event) bool) bool {
	for _, event := range o.snapshot() {
		if predicate(event) {
			return true
		}
	}
	return false
}

func waitCoreClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel")
	}
}
