//go:build integration

package core

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type publicationWaitSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *publicationWaitSink) RecordSendTrace(event sendtrace.Event) {
	if event.Stage != sendtrace.Stage("gateway.session_publication_wait") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) < 8 {
		s.events = append(s.events, event)
	}
}

func (s *publicationWaitSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}

// A ready result owns its original capacity while it waits for an earlier
// session head, and the observation describes that wait rather than append time.
func TestDeferredSendPublicationObservationPreservesSessionHead(t *testing.T) {
	sink := &publicationWaitSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()
	h := &deferredProbeHandler{calls: make(chan deferredProbeCall, 3)}
	e, s := deferredTestExecutor(t, 3, h)
	session, independent := deferredTestState(s, 1), deferredTestState(s, 2)
	submit := func(state *sessionState, id string) deferredProbeCall {
		t.Helper()
		if !e.submit(state, "", &frame.SendPacket{ClientMsgNo: id}) {
			t.Fatal("unexpected admission rejection")
		}
		return deferredNext(t, h)
	}
	first := submit(session, "head")
	second := submit(session, "ready")
	other := submit(independent, "independent")
	var order []string
	write := func(id string) func() error {
		return func() error { order = append(order, id); return nil }
	}
	beforeReady := time.Now()
	if err := second.publish(0, write("ready")); err != nil {
		t.Fatal(err)
	}
	second.complete(nil)
	afterReady := time.Now()
	if len(order) != 0 || len(sink.snapshot()) != 0 || e.depth() != 3 {
		t.Fatal("a ready result published or released before its session head")
	}
	if err := other.publish(0, write("independent")); err != nil {
		t.Fatal(err)
	}
	other.complete(nil)
	if !reflect.DeepEqual(order, []string{"independent"}) {
		t.Fatal("independent session was blocked")
	}
	beforeRelease := time.Now()
	if err := first.publish(0, write("head")); err != nil {
		t.Fatal(err)
	}
	first.complete(nil)
	deferredDrain(t, e)
	if !reflect.DeepEqual(order, []string{"independent", "head", "ready"}) {
		t.Fatal("publication order changed")
	}
	events := sink.snapshot()
	if len(events) != 3 {
		t.Fatalf("publication wait samples = %d, want one per original record", len(events))
	}
	var ready *sendtrace.Event
	for i := range events {
		if events[i].TraceID != "" {
			t.Fatal("core invented a business TraceID")
		}
		if events[i].ClientMsgNo == "ready" {
			if ready != nil {
				t.Fatal("duplicate ready-record sample")
			}
			ready = &events[i]
		}
	}
	if ready == nil || ready.At.Before(beforeReady) || ready.At.After(afterReady) || ready.Duration < beforeRelease.Sub(afterReady) {
		t.Fatal("sample lost the original result-ready boundary or head wait")
	}
}
