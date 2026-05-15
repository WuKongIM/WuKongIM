package plugin

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/wkrpc"
)

var errEmptyPluginNumber = errors.New("empty plugin number")

type rpcContext interface {
	Context() context.Context
	Body() []byte
	Uid() string
	Write([]byte)
	WriteOk()
	WriteErr(error)
}

type wkrpcContext struct {
	ctx *wkrpc.Context
}

func (w wkrpcContext) Context() context.Context { return context.Background() }
func (w wkrpcContext) Body() []byte             { return w.ctx.Body() }
func (w wkrpcContext) Uid() string              { return w.ctx.Uid() }
func (w wkrpcContext) Write(data []byte)        { w.ctx.Write(data) }
func (w wkrpcContext) WriteOk()                 { w.ctx.WriteOk() }
func (w wkrpcContext) WriteErr(err error)       { w.ctx.WriteErr(err) }

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
	if info.No == "" {
		c.WriteErr(errEmptyPluginNumber)
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
	pluginNo := c.Uid()
	if pluginNo == "" {
		c.WriteErr(errEmptyPluginNumber)
		return
	}
	ctx, cancel := s.usecaseContext(c)
	defer cancel()
	if err := s.usecase.ClosePlugin(ctx, pluginNo, c.Uid()); err != nil {
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (s *Server) handleSendMessage(c rpcContext) {
	var req pluginproto.SendReq
	if !s.decodeProto(c, &req) {
		return
	}
	ctx, cancel := s.usecaseContext(c)
	defer cancel()
	resp, err := s.usecase.SendMessage(ctx, &req, c.Uid())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.writeProto(c, resp)
}

func (s *Server) handleChannelMessages(c rpcContext) {
	var req pluginproto.ChannelMessageBatchReq
	if !s.decodeProto(c, &req) {
		return
	}
	ctx, cancel := s.usecaseContext(c)
	defer cancel()
	resp, err := s.usecase.ChannelMessages(ctx, &req, c.Uid())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.writeProto(c, resp)
}

func (s *Server) handleHTTPForward(c rpcContext) {
	var req pluginproto.ForwardHttpReq
	if !s.decodeProto(c, &req) {
		return
	}
	ctx, cancel := s.usecaseContext(c)
	defer cancel()
	resp, err := s.usecase.HTTPForward(ctx, &req, c.Uid())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.writeProto(c, resp)
}

func (s *Server) handleClusterConfig(c rpcContext) {
	if !s.checkBodyLimit(c) {
		return
	}
	ctx, cancel := s.usecaseContext(c)
	defer cancel()
	resp, err := s.usecase.ClusterConfig(ctx, c.Uid())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.writeProto(c, resp)
}

func (s *Server) handleClusterChannelsBelongNode(c rpcContext) {
	var req pluginproto.ClusterChannelBelongNodeReq
	if !s.decodeProto(c, &req) {
		return
	}
	ctx, cancel := s.usecaseContext(c)
	defer cancel()
	resp, err := s.usecase.ClusterChannelsBelongNode(ctx, &req, c.Uid())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.writeProto(c, resp)
}

func (s *Server) handleConversationChannels(c rpcContext) {
	var req pluginproto.ConversationChannelReq
	if !s.decodeProto(c, &req) {
		return
	}
	ctx, cancel := s.usecaseContext(c)
	defer cancel()
	resp, err := s.usecase.ConversationChannels(ctx, &req, c.Uid())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.writeProto(c, resp)
}

func (s *Server) handleUnimplementedStream(c rpcContext) {
	c.WriteErr(ErrUnimplementedStreamRPC)
}
