package plugin

import "github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"

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
