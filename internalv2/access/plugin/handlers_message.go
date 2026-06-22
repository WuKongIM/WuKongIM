package plugin

import "github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"

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
