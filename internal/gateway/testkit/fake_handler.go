package testkit

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type ListenerError struct {
	Listener string
	Err      error
}

type RecordingHandler struct {
	mu sync.Mutex

	CallOrder      []string
	ListenerErrors []ListenerError
	SessionErrors  []error
	CloseReasons   []gateway.CloseReason
	ReplyTokens    []string
	Frames         []frame.Frame
	Contexts       []gateway.Context

	OnSessionOpenErr  error
	OnFrameErr        error
	OnSessionCloseErr error
}

func NewRecordingHandler() *RecordingHandler {
	return &RecordingHandler{}
}

func (h *RecordingHandler) OnListenerError(listener string, err error) {
	if h == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.CallOrder = append(h.CallOrder, "listener_error")
	h.ListenerErrors = append(h.ListenerErrors, ListenerError{Listener: listener, Err: err})
}

func (h *RecordingHandler) OnSessionOpen(ctx *gateway.Context) error {
	if h == nil {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.CallOrder = append(h.CallOrder, "open")
	if ctx != nil {
		h.Contexts = append(h.Contexts, *ctx)
	}
	return h.OnSessionOpenErr
}

func (h *RecordingHandler) OnFrame(ctx *gateway.Context, f frame.Frame) error {
	if h == nil {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.CallOrder = append(h.CallOrder, "frame")
	if ctx != nil {
		h.Contexts = append(h.Contexts, *ctx)
		h.ReplyTokens = append(h.ReplyTokens, ctx.ReplyToken)
		h.CloseReasons = append(h.CloseReasons, ctx.CloseReason)
	}
	h.Frames = append(h.Frames, f)
	return h.OnFrameErr
}

func (h *RecordingHandler) OnSessionClose(ctx *gateway.Context) error {
	if h == nil {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.CallOrder = append(h.CallOrder, "close")
	if ctx != nil {
		h.Contexts = append(h.Contexts, *ctx)
		h.CloseReasons = append(h.CloseReasons, ctx.CloseReason)
	}
	return h.OnSessionCloseErr
}

func (h *RecordingHandler) OnSessionError(ctx *gateway.Context, err error) {
	if h == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.CallOrder = append(h.CallOrder, "error")
	if ctx != nil {
		h.Contexts = append(h.Contexts, *ctx)
		h.CloseReasons = append(h.CloseReasons, ctx.CloseReason)
	}
	h.SessionErrors = append(h.SessionErrors, err)
}

func (h *RecordingHandler) FrameCount() int {
	if h == nil {
		return 0
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	return len(h.Frames)
}

func (h *RecordingHandler) Protocols() []string {
	if h == nil {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]string, 0, len(h.Contexts))
	for _, ctx := range h.Contexts {
		out = append(out, ctx.Protocol)
	}
	return out
}
