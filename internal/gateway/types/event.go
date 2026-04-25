package types

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type Handler interface {
	OnListenerError(listener string, err error)
	OnSessionOpen(ctx *Context) error
	OnFrame(ctx *Context, f frame.Frame) error
	OnSessionClose(ctx *Context) error
	OnSessionError(ctx *Context, err error)
}

type SessionActivator interface {
	OnSessionActivate(ctx *Context) (*frame.ConnackPacket, error)
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
}

func (ctx *Context) WriteFrame(f frame.Frame) error {
	if ctx == nil || ctx.Session == nil {
		return session.ErrSessionClosed
	}
	return ctx.Session.WriteFrame(f, session.WithReplyToken(ctx.ReplyToken))
}
