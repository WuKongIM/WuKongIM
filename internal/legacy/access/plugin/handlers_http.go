package plugin

import "github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"

func (s *Server) handleHTTPForward(c rpcContext) {
	var req pluginproto.ForwardHttpReq
	if !s.decodeProto(c, &req) {
		return
	}
	if req.PluginNo == "" {
		req.PluginNo = c.Uid()
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
