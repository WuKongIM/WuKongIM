//go:build integration

package message

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

type resultPublicationWaitSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *resultPublicationWaitSink) RecordSendTrace(event sendtrace.Event) {
	if event.Stage != sendtrace.Stage("message.result_publication_wait") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) < 8 {
		s.events = append(s.events, event)
	}
}

func (s *resultPublicationWaitSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}

// Result observation must distinguish a completed independent Channel from a
// result still waiting to become publishable in the original physical session.
func TestSendBatchPublicationObservationPreservesReadyResult(t *testing.T) {
	sink := &resultPublicationWaitSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()
	items := asyncMessageItems(3)
	for i := range items {
		items[i].Command.TraceID = []string{"trace-head", "trace-ready", "trace-other"}[i]
	}
	items[2].Command.SenderSessionID = 3
	var order []int
	completed, canceled := 0, 0
	p := newSendBatchPublication(items, func(i int, _ SendBatchItemResult) error { order = append(order, i); return nil }, func() { canceled++ }, func(error) { completed++ })
	beforeReady := time.Now()
	p.finishItem(1, SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}})
	afterReady := time.Now()
	if len(order) != 0 || len(sink.snapshot()) != 0 {
		t.Fatal("same-session ready result escaped its earlier head")
	}
	p.finishItem(2, SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}})
	if !reflect.DeepEqual(order, []int{2}) {
		t.Fatal("independent physical session was blocked")
	}
	beforeRelease := time.Now()
	p.finishItem(0, SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}})
	if err := p.finishPreparation(nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []int{2, 0, 1}) || completed != 1 || canceled != 1 {
		t.Fatal("observation changed original ordering or ownership completion")
	}
	events := sink.snapshot()
	if len(events) != 3 {
		t.Fatalf("result publication samples = %d, want one per original item", len(events))
	}
	var ready *sendtrace.Event
	for i := range events {
		if events[i].ClientMsgNo == items[1].Command.ClientMsgNo {
			if ready != nil {
				t.Fatal("duplicate ready-item sample")
			}
			ready = &events[i]
		}
	}
	if ready == nil || ready.TraceID != "trace-ready" || ready.At.Before(beforeReady) || ready.At.After(afterReady) || ready.Duration < beforeRelease.Sub(afterReady) {
		t.Fatal("sample lost its original ready boundary, TraceID or session wait")
	}
}
