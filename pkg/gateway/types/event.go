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

type SessionActivator interface {
	OnSessionActivate(ctx *Context) (*frame.ConnackPacket, error)
}

// SessionActivationRollbacker is optionally implemented by handlers that must undo a successful activation.
type SessionActivationRollbacker interface {
	OnSessionActivateRollback(ctx Context, err error)
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
}

func (ctx *Context) WriteFrame(f frame.Frame) error {
	if ctx == nil || ctx.Session == nil {
		return session.ErrSessionClosed
	}
	return ctx.Session.WriteFrame(f, session.WithReplyToken(ctx.ReplyToken))
}

// CloseSession closes the gateway session through the physical connection path when available.
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
