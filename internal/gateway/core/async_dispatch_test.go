package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestServerAsyncSendDispatchFallsBackWhenQueueFull(t *testing.T) {
	handler := &countingAsyncFrameHandler{}
	srv := &Server{dispatcher: newDispatcher(handler)}
	queue := newAsyncDispatchQueue(1)
	srv.asyncDispatch = queue
	queue.tasks <- asyncDispatchTask{}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}

	done := make(chan struct{})
	go func() {
		srv.dispatchFrameAsync(state, "", &frame.SendPacket{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Millisecond):
		<-queue.tasks
		<-done
		t.Fatal("dispatchFrameAsync blocked when async queue was full")
	}

	if got := handler.frames.Load(); got != 1 {
		t.Fatalf("handler frames = %d, want fallback synchronous dispatch", got)
	}
}

func TestServerAsyncSendDispatchUsesConfiguredWorkerCount(t *testing.T) {
	srv := &Server{
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendDispatch:        true,
				AsyncSendDispatchWorkers: 4,
			},
		},
	}

	srv.startAsyncDispatcher()
	queue := srv.asyncDispatcher()
	if queue == nil {
		t.Fatal("async dispatcher was not started")
	}
	if got, want := cap(queue.tasks), 4*asyncDispatchQueuePerWorker; got != want {
		t.Fatalf("async dispatch queue capacity = %d, want %d", got, want)
	}

	queue.close()
	srv.workerWG.Wait()
}

func TestAsyncDispatchWorkerCountUsesExplicitOverride(t *testing.T) {
	got := asyncDispatchWorkerCount(gatewaytypes.SessionOptions{AsyncSendDispatchWorkers: 7})
	if got != 7 {
		t.Fatalf("worker count = %d, want explicit override 7", got)
	}
}

func TestAdaptiveAsyncDispatchWorkerCountScalesAndClamps(t *testing.T) {
	tests := []struct {
		name       string
		gomaxprocs int
		want       int
	}{
		{name: "low clamps to minimum", gomaxprocs: 1, want: 64},
		{name: "scales by cpu", gomaxprocs: 10, want: 640},
		{name: "high caps maximum", gomaxprocs: 128, want: 1024},
		{name: "invalid clamps minimum", gomaxprocs: 0, want: 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptiveAsyncDispatchWorkerCount(tt.gomaxprocs); got != tt.want {
				t.Fatalf("adaptive worker count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRecordAsyncDispatchWaitIncludesSendClientMsgNo(t *testing.T) {
	sink := &recordingSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	recordAsyncDispatchWait(asyncDispatchTask{
		enqueuedAt: time.Now().Add(-time.Millisecond),
		frame: &frame.SendPacket{
			ClientMsgNo: "async-wait-1",
		},
	})

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("recorded events = %d, want 1", len(events))
	}
	if got := events[0].Stage; got != sendtrace.StageGatewayAsyncDispatchWait {
		t.Fatalf("stage = %s, want %s", got, sendtrace.StageGatewayAsyncDispatchWait)
	}
	if got := events[0].ClientMsgNo; got != "async-wait-1" {
		t.Fatalf("client msg no = %q, want async-wait-1", got)
	}
	if events[0].Duration <= 0 {
		t.Fatalf("duration = %s, want > 0", events[0].Duration)
	}
}

type countingAsyncFrameHandler struct {
	frames atomic.Uint64
}

type recordingSendTraceSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *recordingSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSendTraceSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}

func (h *countingAsyncFrameHandler) OnListenerError(string, error) {}
func (h *countingAsyncFrameHandler) OnSessionOpen(*gatewaytypes.Context) error {
	return nil
}
func (h *countingAsyncFrameHandler) OnFrame(*gatewaytypes.Context, frame.Frame) error {
	h.frames.Add(1)
	return nil
}
func (h *countingAsyncFrameHandler) OnSessionClose(*gatewaytypes.Context) error {
	return nil
}
func (h *countingAsyncFrameHandler) OnSessionError(*gatewaytypes.Context, error) {}

func BenchmarkServerAsyncSendDispatchQueueFullFallback(b *testing.B) {
	handler := &countingAsyncFrameHandler{}
	srv := &Server{dispatcher: newDispatcher(handler)}
	queue := newAsyncDispatchQueue(1)
	srv.asyncDispatch = queue
	queue.tasks <- asyncDispatchTask{}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	packet := &frame.SendPacket{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.dispatchFrameAsync(state, "", packet)
	}
	b.StopTimer()
	if got := handler.frames.Load(); got != uint64(b.N) {
		b.Fatalf("handler frames = %d, want %d", got, b.N)
	}
}
