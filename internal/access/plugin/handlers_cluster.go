package plugin

import "github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"

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
