package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestServerAsyncSendDispatchRejectsWhenQueueFull(t *testing.T) {
	handler := &countingAsyncFrameHandler{}
	srv := &Server{dispatcher: newDispatcher(handler)}
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	srv.asyncDispatch = queue
	queue.shards[0].tasks <- asyncDispatchTask{}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	state.markOpenDispatched()

	done := make(chan struct{})
	go func() {
		srv.dispatchSendFrameAsync(state, "", &frame.SendPacket{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Millisecond):
		<-queue.shards[0].tasks
		<-done
		t.Fatal("dispatchSendFrameAsync blocked when async queue was full")
	}

	if got := handler.frames.Load(); got != 0 {
		t.Fatalf("handler frames = %d, want async queue full rejection without synchronous fallback", got)
	}
	if !state.isClosed() {
		t.Fatal("state was not closed after async queue overflow")
	}
	errs := handler.sessionErrors()
	if len(errs) != 1 || !errors.Is(errs[0], gatewaytypes.ErrAsyncDispatchQueueFull) {
		t.Fatalf("session errors = %v, want ErrAsyncDispatchQueueFull", errs)
	}
	reasons := handler.closeReasons()
	if len(reasons) == 0 || reasons[0] != gatewaytypes.CloseReasonAsyncDispatchQueueFull {
		t.Fatalf("close reasons = %v, want %q", reasons, gatewaytypes.CloseReasonAsyncDispatchQueueFull)
	}
}

func TestServerAsyncSendDispatchRejectsFullQueueBeforePayloadClone(t *testing.T) {
	srv := &Server{dispatcher: newDispatcher(&countingAsyncFrameHandler{})}
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	srv.asyncDispatch = queue
	queue.shards[0].tasks <- asyncDispatchTask{}

	payload := make([]byte, 64*1024)
	packet := &frame.SendPacket{Payload: payload}
	newState := func() *sessionState {
		return &sessionState{
			server:         srv,
			closedCh:       make(chan struct{}),
			requestContext: context.Background(),
		}
	}

	baseline := testing.AllocsPerRun(1000, func() {
		newState().close(gatewaytypes.CloseReasonAsyncDispatchQueueFull, gatewaytypes.ErrAsyncDispatchQueueFull)
	})
	actual := testing.AllocsPerRun(1000, func() {
		srv.dispatchSendFrameAsync(newState(), "", packet)
	})
	if actual > baseline+0.5 {
		t.Fatalf("allocs = %.2f, want near close baseline %.2f; full async queue should not clone payload", actual, baseline)
	}
}

func TestServerAsyncSendDispatchUsesConfiguredWorkerCount(t *testing.T) {
	srv := &Server{
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendDispatchWorkers: 4,
			},
		},
	}

	srv.startAsyncDispatcher()
	queue := srv.asyncDispatcher()
	if queue == nil {
		t.Fatal("async dispatcher was not started")
	}
	if got, want := len(queue.shards), 4; got != want {
		t.Fatalf("async dispatch shards = %d, want %d", got, want)
	}
	for i, shard := range queue.shards {
		if got, want := cap(shard.tasks), asyncDispatchQueuePerWorker; got != want {
			t.Fatalf("async dispatch shard %d capacity = %d, want %d", i, got, want)
		}
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

	mu          sync.Mutex
	sessionErrs []error
	closeLog    []gatewaytypes.CloseReason
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
func (h *countingAsyncFrameHandler) OnSessionClose(ctx *gatewaytypes.Context) error {
	if ctx != nil {
		h.mu.Lock()
		h.closeLog = append(h.closeLog, ctx.CloseReason)
		h.mu.Unlock()
	}
	return nil
}
func (h *countingAsyncFrameHandler) OnSessionError(ctx *gatewaytypes.Context, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionErrs = append(h.sessionErrs, err)
	if ctx != nil {
		h.closeLog = append(h.closeLog, ctx.CloseReason)
	}
}

func (h *countingAsyncFrameHandler) sessionErrors() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]error(nil), h.sessionErrs...)
}

func (h *countingAsyncFrameHandler) closeReasons() []gatewaytypes.CloseReason {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gatewaytypes.CloseReason(nil), h.closeLog...)
}

func BenchmarkServerAsyncSendDispatchQueueFullReject(b *testing.B) {
	handler := &countingAsyncFrameHandler{}
	srv := &Server{dispatcher: newDispatcher(handler)}
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	srv.asyncDispatch = queue
	queue.shards[0].tasks <- asyncDispatchTask{}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	state.markOpenDispatched()
	packet := &frame.SendPacket{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.dispatchSendFrameAsync(state, "", packet)
	}
	b.StopTimer()
	if got := handler.frames.Load(); got != 0 {
		b.Fatalf("handler frames = %d, want 0", got)
	}
}
