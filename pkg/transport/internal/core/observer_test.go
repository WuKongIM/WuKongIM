package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func TestObserverDrainDoesNotBlockWhenSinkIsSlow(t *testing.T) {
	sink := &blockingObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	drain := newTestObserverDrain(sink)
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
	drain := newTestObserverDrain(sink)
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

func TestObserverDrainIgnoresEventsAfterStop(t *testing.T) {
	sink := &countingObserver{}
	drain := newTestObserverDrain(sink)
	drain.Stop()

	for i := 0; i < 100; i++ {
		drain.ObserveTransport(Event{Name: "rpc", Result: "ok"})
	}
	if got := sink.count.Load(); got != 0 {
		t.Fatalf("events observed after stop = %d, want 0", got)
	}
}

func TestObserverDrainStopWaitsForAdmittedTerminalEvent(t *testing.T) {
	sink := &recordingBlockingObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	drain := newTestObserverDrain(sink)

	drain.ObserveTransport(Event{Name: "first"})
	waitCoreClosed(t, sink.entered)
	for i := 0; i < defaultObserverQueueSize; i++ {
		drain.ObserveTransport(Event{Name: "overflow"})
	}

	terminal := Event{Name: "scheduler_queue", SourceID: 99, Result: "stopped"}
	terminalDone := make(chan struct{})
	go func() {
		drain.ObserveTransport(terminal)
		close(terminalDone)
	}()
	waitCoreAdmissionCount(t, drain, 1)
	stopDone := make(chan struct{})
	go func() {
		drain.Stop()
		close(stopDone)
	}()

	waitCoreStopped(t, drain)
	assertCoreNotClosed(t, terminalDone)
	assertCoreNotClosed(t, stopDone)
	close(sink.release)
	waitCoreClosed(t, terminalDone)
	waitCoreClosed(t, stopDone)

	if !sink.hasEvent(func(event Event) bool {
		return event == terminal
	}) {
		t.Fatal("terminal event admitted before Stop was not delivered")
	}
	if queued := len(drain.events); queued != 0 {
		t.Fatalf("events queued after stop = %d, want 0", queued)
	}
}

func BenchmarkObserverDrainObserveNonTerminal(b *testing.B) {
	drain := newTestObserverDrain(&countingObserver{})
	event := Event{Name: "rpc", Result: "ok", Items: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		drain.ObserveTransport(event)
	}
	b.StopTimer()
	drain.Stop()
}

func newTestObserverDrain(target Observer) *ObserverDrain {
	return NewObserverDrain(target, goruntimeregistry.TaskTransportClientObserver)
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

type countingObserver struct {
	count atomic.Int64
}

func (o *countingObserver) ObserveTransport(Event) {
	o.count.Add(1)
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

func assertCoreNotClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("channel closed before blocked operation was released")
	case <-time.After(25 * time.Millisecond):
	}
}

func waitCoreAdmissionCount(t *testing.T, drain *ObserverDrain, want uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := drain.admission.Load() & observerDrainActiveMask; got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for admission count %d", want)
}

func waitCoreStopped(t *testing.T, drain *ObserverDrain) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if drain.admission.Load()&observerDrainStoppedBit != 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for ObserverDrain to stop admissions")
}
