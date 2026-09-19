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

func TestObserverDrainPreservesLatestQueueStateWhenEventQueueIsFull(t *testing.T) {
	sink := &recordingBlockingObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	drain := newTestObserverDrain(sink)

	drain.ObserveTransport(Event{Name: "first"})
	waitCoreClosed(t, sink.entered)
	for i := 0; i < defaultObserverQueueSize*2; i++ {
		drain.ObserveTransport(Event{Name: "overflow"})
	}

	drain.ObserveTransport(Event{
		Name:     "scheduler_queue",
		SourceID: 99,
		Priority: PriorityRPC,
		Result:   "ok",
		Revision: 1,
		Items:    8,
	})
	drain.ObserveTransport(Event{
		Name:     "scheduler_queue",
		SourceID: 99,
		Priority: PriorityRPC,
		Result:   "ok",
		Revision: 2,
		Items:    0,
	})
	drain.ObserveTransport(Event{
		Name:     "scheduler_queue",
		SourceID: 99,
		Priority: PriorityRPC,
		Result:   "ok",
		Revision: 1,
		Items:    8,
	})
	drain.ObserveTransport(Event{
		Name:         "service_queue",
		ServiceID:    7,
		ServiceAlias: "slot channel metadata",
		Result:       "ok",
		Revision:     1,
		Items:        2,
	})
	drain.ObserveTransport(Event{
		Name:         "service_queue",
		ServiceID:    7,
		ServiceAlias: "slot channel metadata",
		Result:       "ok",
		Revision:     2,
		Items:        0,
	})
	drain.ObserveTransport(Event{
		Name:         "service_queue",
		ServiceID:    7,
		ServiceAlias: "slot channel metadata",
		Result:       "ok",
		Revision:     1,
		Items:        2,
	})

	close(sink.release)
	drain.Stop()

	assertLastState := func(name string, matches func(Event) bool) {
		t.Helper()
		var (
			last  Event
			found bool
		)
		for _, event := range sink.snapshot() {
			if matches(event) {
				last = event
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s state event", name)
		}
		if last.Items != 0 {
			t.Fatalf("last %s items = %d, want 0", name, last.Items)
		}
	}
	assertLastState("scheduler queue", func(event Event) bool {
		return event.Name == "scheduler_queue" && event.SourceID == 99 && event.Priority == PriorityRPC
	})
	assertLastState("service queue", func(event Event) bool {
		return event.Name == "service_queue" && event.ServiceID == 7
	})
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

func TestObserverDrainStopDrainsTerminalStateAdmittedBeforeStop(t *testing.T) {
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
	waitCoreClosed(t, terminalDone)
	stopDone := make(chan struct{})
	go func() {
		drain.Stop()
		close(stopDone)
	}()

	waitCoreStopped(t, drain)
	assertCoreNotClosed(t, stopDone)
	close(sink.release)
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

// Repeated absolute-state updates must reuse bounded source storage even while
// the observer is stalled; every RPC publishes several of these observations.
func TestObserverDrainStateUpdatesReuseStorage(t *testing.T) {
	sink := &blockingObserver{entered: make(chan struct{}), release: make(chan struct{})}
	drain := newTestObserverDrain(sink)
	defer func() { close(sink.release); drain.Stop() }()
	drain.ObserveTransport(Event{Name: "first"})
	waitCoreClosed(t, sink.entered)
	event := Event{Name: "pending_rpc", SourceID: 1, Revision: 1, Items: 1}
	drain.ObserveTransport(event)
	allocs := testing.AllocsPerRun(100, func() {
		event.Revision++
		drain.ObserveTransport(event)
	})
	if allocs != 0 {
		t.Fatalf("steady state allocations per update = %g, want 0", allocs)
	}
}

func TestObserverDrainStateDeliveryReusesStorage(t *testing.T) {
	delivered := make(chan Event)
	drain := newTestObserverDrain(observerFunc(func(event Event) { delivered <- event }))
	defer drain.Stop()
	event := Event{Name: "pending_rpc", SourceID: 1, Items: 1}
	allocs := testing.AllocsPerRun(100, func() {
		event.Revision++
		drain.ObserveTransport(event)
		if got := <-delivered; got != event {
			t.Fatalf("delivered %+v, want %+v", got, event)
		}
	})
	if allocs != 0 {
		t.Fatalf("steady state allocations per delivery = %g, want 0", allocs)
	}
}

func TestObserverDrainRevisionSurvivesDelivery(t *testing.T) {
	delivered := make(chan Event, 8)
	drain := newTestObserverDrain(observerFunc(func(event Event) { delivered <- event }))
	defer drain.Stop()
	event := Event{Name: "service_inflight", ServiceID: 7, Revision: 2, Inflight: 1}
	drain.ObserveTransport(event)
	if got := <-delivered; got != event {
		t.Fatalf("first = %+v", got)
	}
	// Unversioned observations still follow arrival order without forgetting the
	// last versioned state, even after the preceding batch was delivered.
	event.Revision = 0
	event.Inflight = 0
	drain.ObserveTransport(event)
	if got := <-delivered; got != event {
		t.Fatalf("unversioned = %+v", got)
	}
	event.Revision = 1
	event.Inflight = 9
	drain.ObserveTransport(event)
	event.Revision = 3
	event.Inflight = 0
	drain.ObserveTransport(event)
	if got := <-delivered; got != event {
		t.Fatalf("latest = %+v, want %+v", got, event)
	}
}

func TestObserverDrainConcurrentSourcesPreserveFinalState(t *testing.T) {
	last := make(map[uint64]Event)
	drain := newTestObserverDrain(observerFunc(func(event Event) { last[event.SourceID] = event }))
	var writers sync.WaitGroup
	for source := uint64(1); source <= 16; source++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for revision := uint64(1); revision <= 1000; revision++ {
				drain.ObserveTransport(Event{Name: "pending_rpc", SourceID: source, Revision: revision, Inflight: int(1000 - revision)})
			}
		}()
	}
	writers.Wait()
	drain.Stop()
	if len(last) != 16 {
		t.Fatalf("delivered sources = %d", len(last))
	}
	for source, event := range last {
		if event.Revision != 1000 || event.Inflight != 0 {
			t.Fatalf("source %d final = %+v", source, event)
		}
	}
}

func TestObserverDrainStateStorageRemainsBounded(t *testing.T) {
	sink := &recordingBlockingObserver{entered: make(chan struct{}), release: make(chan struct{})}
	drain := newTestObserverDrain(sink)
	drain.ObserveTransport(Event{Name: "first"})
	waitCoreClosed(t, sink.entered)
	for source := uint64(1); source <= maxObserverCoalescedStateKeys+1; source++ {
		drain.ObserveTransport(Event{Name: "pending_rpc", SourceID: source, Revision: 1, Items: 1})
	}
	// A new source at capacity falls back to the ordinary bounded queue. Existing
	// sources must continue accepting the newest state, including final zero.
	for source := uint64(1); source <= maxObserverCoalescedStateKeys; source++ {
		drain.ObserveTransport(Event{Name: "pending_rpc", SourceID: source, Revision: 2})
	}
	drain.stateMu.Lock()
	states, pending := len(drain.states), len(drain.pending)
	drain.stateMu.Unlock()
	close(sink.release)
	drain.Stop()
	if states != maxObserverCoalescedStateKeys || pending != maxObserverCoalescedStateKeys {
		t.Fatalf("state storage grew past bound: states=%d pending=%d", states, pending)
	}
	last := make(map[uint64]Event)
	for _, event := range sink.snapshot() {
		if event.Name == "pending_rpc" {
			last[event.SourceID] = event
		}
	}
	if len(last) != maxObserverCoalescedStateKeys+1 {
		t.Fatalf("delivered sources = %d", len(last))
	}
	for source := uint64(1); source <= maxObserverCoalescedStateKeys; source++ {
		if last[source].Revision != 2 || last[source].Items != 0 {
			t.Fatalf("source %d final = %+v", source, last[source])
		}
	}
}

type observerFunc func(Event)

func (f observerFunc) ObserveTransport(event Event) { f(event) }

func BenchmarkObserverDrainState(b *testing.B) {
	drain := newTestObserverDrain(&countingObserver{})
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		event := Event{Name: "pending_rpc", SourceID: NextStateRevision(), Items: 1}
		for pb.Next() {
			event.Revision = NextStateRevision()
			drain.ObserveTransport(event)
		}
	})
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
