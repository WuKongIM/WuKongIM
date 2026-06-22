package plugin

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/wkrpc"
)

var errEmptyPluginNumber = errors.New("empty plugin number")
var errEmptyCallerUID = errors.New("empty caller uid")

type rpcContext interface {
	Context() context.Context
	Body() []byte
	Uid() string
	Write([]byte)
	WriteOk()
	WriteErr(error)
}

type closeEventContext interface {
	CloseEvent() bool
}

type wkrpcContext struct {
	ctx *wkrpc.Context
}

func (w wkrpcContext) Context() context.Context { return context.Background() }
func (w wkrpcContext) Body() []byte {
	body, _ := w.safeBody()
	return body
}
func (w wkrpcContext) Uid() string        { return w.ctx.Uid() }
func (w wkrpcContext) Write(data []byte)  { w.ctx.Write(data) }
func (w wkrpcContext) WriteOk()           { w.ctx.WriteOk() }
func (w wkrpcContext) WriteErr(err error) { w.ctx.WriteErr(err) }
func (w wkrpcContext) CloseEvent() bool {
	_, closeEvent := w.safeBody()
	return closeEvent
}

func (w wkrpcContext) safeBody() (body []byte, closeEvent bool) {
	defer func() {
		if recover() != nil {
			body = nil
			closeEvent = true
		}
	}()
	return w.ctx.Body(), false
}

func (s *Server) usecaseContext(c rpcContext) (context.Context, context.CancelFunc) {
	base := c.Context()
	if base == nil {
		base = context.Background()
	}
	if deadline, ok := base.Deadline(); ok && deadline.Sub(s.now()) <= s.timeout {
		return base, func() {}
	}
	return context.WithTimeout(base, s.timeout)
}

func (s *Server) handlePluginStart(c rpcContext) {
	var info pluginproto.PluginInfo
	if !s.decodeProto(c, &info) {
		return
	}
	if info.GetNo() == "" {
		c.WriteErr(errEmptyPluginNumber)
		return
	}
	if c.Uid() == "" {
		c.WriteErr(errEmptyCallerUID)
		return
	}
	ctx, cancel := s.usecaseContext(c)
	defer cancel()
	resp, err := s.usecase.StartPlugin(ctx, &info, c.Uid())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.writeProto(c, resp)
}

func (s *Server) handleClose(c rpcContext) {
	if !s.checkBodyLimit(c) {
		return
	}
	pluginNo := c.Uid()
	if pluginNo == "" {
		c.WriteErr(errEmptyPluginNumber)
		return
	}
	callerUID := pluginNo
	ctx, cancel := s.usecaseContext(c)
	if isCloseEvent(c) {
		go func() {
			defer cancel()
			_ = s.usecase.ClosePlugin(ctx, pluginNo, callerUID)
		}()
		return
	}
	defer cancel()
	if err := s.usecase.ClosePlugin(ctx, pluginNo, callerUID); err != nil {
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func isCloseEvent(c rpcContext) bool {
	eventCtx, ok := c.(closeEventContext)
	return ok && eventCtx.CloseEvent()
}
