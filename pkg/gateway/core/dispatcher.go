package core

import (
	"context"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type dispatcher struct {
	handler         gatewaytypes.Handler
	batchHandler    gatewaytypes.SendBatchHandler
	deferredHandler gatewaytypes.DeferredSendBatchHandler
}

func newDispatcher(handler gatewaytypes.Handler) dispatcher {
	dispatcher := dispatcher{handler: handler}
	if batchHandler, ok := handler.(gatewaytypes.SendBatchHandler); ok {
		dispatcher.batchHandler = batchHandler
	}
	dispatcher.deferredHandler, _ = handler.(gatewaytypes.DeferredSendBatchHandler)
	return dispatcher
}

func (d dispatcher) listenerError(listener string, err error) {
	if d.handler == nil || err == nil {
		return
	}
	d.handler.OnListenerError(listener, err)
}

func (d dispatcher) sessionOpen(state *sessionState) error {
	if h := state.packetHandler(); h != nil {
		return callPacketCallback(func() error {
			return h.OnSessionOpen(d.context(state, "", state.closeReason(), d.requestContext(state)))
		})
	}
	if d.handler == nil {
		return nil
	}
	return d.handler.OnSessionOpen(d.context(state, "", state.closeReason(), nil))
}

func (d dispatcher) frame(state *sessionState, replyToken string, f frame.Frame) error {
	if d.handler == nil {
		return nil
	}
	return d.handler.OnFrame(d.context(state, replyToken, state.closeReason(), d.requestContext(state)), f)
}

func (d dispatcher) sendBatch(items []gatewaytypes.SendBatchItem) (bool, error) {
	if d.batchHandler == nil {
		return false, nil
	}
	return true, d.batchHandler.OnSendBatch(items)
}

func (d dispatcher) canSendBatch() bool {
	return d.batchHandler != nil
}

func (d dispatcher) sessionError(state *sessionState, reason gatewaytypes.CloseReason, err error) {
	if h := state.packetHandler(); h != nil {
		if err != nil {
			_ = callPacketCallback(func() error { h.OnSessionError(d.context(state, "", reason, nil), err); return nil })
		}
		return
	}
	if d.handler == nil || err == nil {
		return
	}
	d.handler.OnSessionError(d.context(state, "", reason, nil), err)
}

func (d dispatcher) sessionClose(state *sessionState) error {
	if h := state.packetHandler(); h != nil {
		return callPacketCallback(func() error { return h.OnSessionClose(d.context(state, "", state.closeReason(), nil)) })
	}
	if d.handler == nil {
		return nil
	}
	return d.handler.OnSessionClose(d.context(state, "", state.closeReason(), nil))
}

func (d dispatcher) context(state *sessionState, replyToken string, reason gatewaytypes.CloseReason, requestContext context.Context) gatewaytypes.Context {
	if state == nil || state.listener == nil {
		return gatewaytypes.Context{CloseReason: reason, ReplyToken: replyToken, RequestContext: requestContext}
	}

	return gatewaytypes.Context{
		Session:        state.session,
		Listener:       state.listener.options.Name,
		Network:        state.listener.options.Network,
		Transport:      state.listener.options.Transport,
		Protocol:       state.protocolName(),
		CloseReason:    reason,
		ReplyToken:     replyToken,
		RequestContext: requestContext,
		CloseSessionFn: func(closeReason gatewaytypes.CloseReason, closeErr error) {
			state.close(closeReason, closeErr)
		},
		TransportCloser: state,
	}
}

func (d dispatcher) requestContext(state *sessionState) context.Context {
	if state != nil && state.requestContext != nil {
		return state.requestContext
	}
	return context.Background()
}
