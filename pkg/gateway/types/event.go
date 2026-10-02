package types

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type Handler interface {
	OnListenerError(listener string, err error)
	OnSessionOpen(ctx Context) error
	OnFrame(ctx Context, f frame.Frame) error
	OnSessionClose(ctx Context) error
	OnSessionError(ctx Context, err error)
}

// SendBatchItem carries one asynchronous SEND frame through a gateway micro-batch.
type SendBatchItem struct {
	// Context is the per-frame gateway context, including request context and reply token.
	Context Context
	// ReplyToken preserves the inbound protocol request token for the matching response.
	ReplyToken string
	// Frame is the cloned SEND frame for this batch item.
	Frame *frame.SendPacket
	// Index is the item's position in the gateway micro-batch.
	Index int
	// EnqueuedAt records when the frame entered the async dispatch queue.
	EnqueuedAt time.Time
	// ByteCount is the payload byte count used for gateway batch limits.
	ByteCount int
}

// SendBatchHandler is optionally implemented by handlers that can process SEND frames in batches.
type SendBatchHandler interface {
	OnSendBatch(items []SendBatchItem) error
}

// DeferredSendBatchHandler optionally joins preparation while transferring result
// completion. publish admits one nonnil publication per index; core invokes it
// in session order across batches. Publication must use normal Session writes so
// terminal seals remain authoritative. publish reports callback-admission errors;
// core handles write failures against the owning session. complete runs once on successful
// admission, after all publish calls; it may run inline. A returned error transfers
// no callbacks. Core owns descriptors/capacity through preparation return, complete,
// and ordered publication. Neither callback may be retained beyond completion.
// Publishers must not recursively invoke callbacks for the same batch/session.
type DeferredSendBatchHandler interface {
	OnSendBatchDeferred(items []SendBatchItem, publish func(int, func() error) error, complete func(error)) error
}

type SessionActivator interface {
	OnSessionActivate(ctx *Context) (*frame.ConnackPacket, error)
}

// SessionActivationRollbacker is optionally implemented by handlers that must undo a successful activation.
type SessionActivationRollbacker interface {
	OnSessionActivateRollback(ctx Context, err error)
}

// TransportCloser isolates a connection without joining lifecycle callbacks.
// It is a per-connection capability, not a per-packet closure.
type TransportCloser interface {
	CloseTransportAndWait(context.Context, CloseReason) error
}

type Context struct {
	Session        session.Session
	Listener       string
	Network        string
	Transport      string
	Protocol       string
	CloseReason    CloseReason
	ReplyToken     string
	RequestContext context.Context
	// CloseSessionFn closes the owning gateway connection state when core builds this context.
	CloseSessionFn func(CloseReason, error)
	// TransportCloser fences admission and joins physical closure only;
	// it must not invoke or wait for protocol/business lifecycle cleanup.
	TransportCloser TransportCloser
}

func (ctx *Context) WriteFrame(f frame.Frame) error {
	if ctx == nil || ctx.Session == nil {
		return session.ErrSessionClosed
	}
	return ctx.Session.WriteFrame(f, session.WithReplyToken(ctx.ReplyToken))
}

// SealOutboundAndWrite atomically seals ordinary writes on the owning session
// and admits f as its final ordered frame. Transport implementations are not
// treated as flushed here; the peer must decode the final frame as the ACK.
func (ctx *Context) SealOutboundAndWrite(f frame.Frame) error {
	if ctx == nil || ctx.Session == nil {
		return session.ErrSessionClosed
	}
	sealer, ok := ctx.Session.(session.OutboundSealer)
	if !ok {
		return session.ErrOutboundSealUnsupported
	}
	return sealer.SealOutboundAndWrite(f, session.WithReplyToken(ctx.ReplyToken))
}

// OutboundSealed reports whether this session has admitted its terminal frame.
// Entry adapters must reject every later ordinary frame before use-case entry.
func (ctx *Context) OutboundSealed() bool {
	if ctx == nil || ctx.Session == nil {
		return false
	}
	state, ok := ctx.Session.(session.OutboundSealState)
	return ok && state.OutboundSealed()
}

// CloseSession requests closure through the owning gateway state when available.
// Success is not proof that the physical connection has finished closing.
func (ctx *Context) CloseSession(reason CloseReason, err error) error {
	if ctx == nil {
		return session.ErrSessionClosed
	}
	if ctx.CloseSessionFn != nil {
		ctx.CloseSessionFn(reason, err)
		return nil
	}
	if ctx.Session == nil {
		return session.ErrSessionClosed
	}
	return ctx.Session.Close()
}

// CloseTransportAndWait requires the optional physical close capability. It
// never substitutes logical closure or lifecycle callbacks for isolation proof.
func (ctx *Context) CloseTransportAndWait(wait context.Context, reason CloseReason) error {
	if ctx == nil || ctx.TransportCloser == nil {
		return ErrCloseProofUnsupported
	}
	return ctx.TransportCloser.CloseTransportAndWait(wait, reason)
}
