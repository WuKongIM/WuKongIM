//go:build integration

package core

import (
	"sync"
	"testing"
	"time"
)

// The public observer lifecycle must not allocate a disposable counter batch
// on every tick, lose accepted counters behind a slow callback, or mutate
// duration samples that a callback retains after it returns.
func TestObserverCounterDeliveryReusesBatchStorage(t *testing.T) {
	delivered := make(chan Event)
	drain := newTestObserverDrain(observerFunc(func(e Event) { delivered <- e }))
	defer drain.Stop()
	event := Event{Name: "sent_bytes", NodeID: 1, Kind: FrameKindRPCRequest, Bytes: 17}
	allocs := testing.AllocsPerRun(32, func() {
		drain.ObserveTransport(event)
		got := <-delivered
		if got.Name != event.Name || got.Count != 1 || got.Bytes != 17 {
			t.Fatalf("counter delivery = %+v, want one 17-byte event", got)
		}
	})
	if allocs != 0 {
		t.Fatalf("steady counter delivery allocations = %g, want 0", allocs)
	}
}

func TestObserverBatchReuseKeepsRetainedDurationSamplesDetached(t *testing.T) {
	delivered := make(chan Event)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	calls := 0
	drain := newTestObserverDrain(observerFunc(func(e Event) {
		calls++
		delivered <- e
		if calls == 1 {
			<-release
		}
	}))
	defer func() { unblock(); drain.Stop() }()
	drain.ObserveTransport(Event{Name: "service_task", ServiceID: 1, Result: "ok", Duration: 42 * time.Millisecond})
	first := <-delivered
	// Hold delivery while all thirty-two later calls enter the same key.
	// This proves the flush boundary without relying on scheduler timing.
	for i := 0; i < 32; i++ {
		drain.ObserveTransport(Event{Name: "service_task", ServiceID: 1, Result: "ok", Duration: 84 * time.Millisecond})
	}
	unblock()
	second := <-delivered
	for i, e := range []Event{first, second} {
		wantDuration := time.Duration(42*(i+1)) * time.Millisecond
		wantCount := uint64(1)
		if i == 1 {
			wantCount = 32
		}
		if e.Count != wantCount || e.Samples == nil || e.Samples.Len != 1 || e.Samples.Values[0] != wantDuration {
			t.Fatalf("retained duration wave %d = %+v samples=%+v", i, e, e.Samples)
		}
	}
	if first.Samples == second.Samples {
		t.Fatal("duration waves share mutable sample storage")
	}
}

func TestObserverSlowAggregateCallbackPreservesStopCounters(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var records []Event
	var once sync.Once
	drain := newTestObserverDrain(observerFunc(func(e Event) {
		once.Do(func() { close(entered); <-release })
		records = append(records, e)
	}))
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() { unblock(); drain.Stop() }()
	drain.ObserveTransport(Event{Name: "sent_bytes", Bytes: 11})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first aggregate callback did not start")
	}
	for i := 0; i < 1000; i++ {
		drain.ObserveTransport(Event{Name: "sent_bytes", Bytes: 11})
	}
	stopped := make(chan struct{})
	go func() { drain.Stop(); close(stopped) }()
	unblock()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not join accepted aggregate delivery")
	}
	var count uint64
	var bytes int
	for _, e := range records {
		if e.Name != "sent_bytes" {
			t.Fatalf("unexpected aggregate event %+v", e)
		}
		count += e.Count
		bytes += e.Bytes
	}
	if count != 1001 || bytes != 11011 {
		t.Fatalf("drained counters = %d/%d, want 1001/11011", count, bytes)
	}
}
