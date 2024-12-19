package server

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

// handleClusterMessage 处理分布式消息（注意：不要再此方法里做耗时操作，如果耗时操作另起协程）
func (s *Server) handleClusterMessage(fromNodeId uint64, msg *proto.Message) {
	if msg.MsgType >= options.ReactorUserMsgTypeMin.Uint32() && msg.MsgType < options.ReactorUserMsgTypeMax.Uint32() {
		s.processUser.OnMessage(msg)
	} else if msg.MsgType >= options.ReactorChannelMsgTypeMin.Uint32() && msg.MsgType < options.ReactorChannelMsgTypeMax.Uint32() {
		s.processChannel.OnMessage(msg)
	} else if msg.MsgType >= uint32(options.ReactorPushMsgTypeMin) && msg.MsgType < uint32(options.ReactorPushMsgTypeMax) {
		s.processPush.OnMessage(msg)
	}
}
