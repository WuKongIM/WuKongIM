package core

import (
	"context"
	"testing"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestDispatcherFrameKeepsRequestContextOpenAfterReturn(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := &requestContextCapturingHandler{}
	state := &sessionState{requestContext: parent}

	if err := newDispatcher(handler).frame(state, "", &frame.PingPacket{}); err != nil {
		t.Fatalf("frame dispatch failed: %v", err)
	}

	if handler.requestContext == nil {
		t.Fatal("handler did not receive request context")
	}
	if err := handler.requestContext.Err(); err != nil {
		t.Fatalf("request context was canceled after frame returned: %v", err)
	}
	cancel()
	if err := handler.requestContext.Err(); err != context.Canceled {
		t.Fatalf("request context did not observe parent cancellation: %v", err)
	}
}

type requestContextCapturingHandler struct {
	requestContext context.Context
}

func (h *requestContextCapturingHandler) OnListenerError(string, error) {}

func (h *requestContextCapturingHandler) OnSessionOpen(gatewaytypes.Context) error {
	return nil
}

func (h *requestContextCapturingHandler) OnFrame(ctx gatewaytypes.Context, _ frame.Frame) error {
	h.requestContext = ctx.RequestContext
	return nil
}

func (h *requestContextCapturingHandler) OnSessionClose(gatewaytypes.Context) error {
	return nil
}

func (h *requestContextCapturingHandler) OnSessionError(gatewaytypes.Context, error) {}
