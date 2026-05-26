package core

import (
	"context"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type dispatcher struct {
	handler      gatewaytypes.Handler
	batchHandler gatewaytypes.SendBatchHandler
}

func newDispatcher(handler gatewaytypes.Handler) dispatcher {
	dispatcher := dispatcher{handler: handler}
	if batchHandler, ok := handler.(gatewaytypes.SendBatchHandler); ok {
		dispatcher.batchHandler = batchHandler
	}
	return dispatcher
}

func (d dispatcher) listenerError(listener string, err error) {
	if d.handler == nil || err == nil {
		return
	}
	d.handler.OnListenerError(listener, err)
}

func (d dispatcher) sessionOpen(state *sessionState) error {
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
	if d.handler == nil || err == nil {
		return
	}
	d.handler.OnSessionError(d.context(state, "", reason, nil), err)
}

func (d dispatcher) sessionClose(state *sessionState) error {
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
	}
}

func (d dispatcher) requestContext(state *sessionState) context.Context {
	if state != nil && state.requestContext != nil {
		return state.requestContext
	}
	return context.Background()
}
