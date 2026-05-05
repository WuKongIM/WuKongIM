package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
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

type countingAsyncFrameHandler struct {
	frames atomic.Uint64
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
