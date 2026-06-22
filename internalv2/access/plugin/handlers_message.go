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
