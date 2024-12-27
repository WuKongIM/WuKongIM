package server

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

// handleClusterMessage 处理分布式消息（注意：不要再此方法里做耗时操作，如果耗时操作另起协程）
func (s *Server) handleClusterMessage(_ uint64, msg *proto.Message) {
	if msg.MsgType >= uint32(eventbus.UserEventMsgMin) && msg.MsgType < uint32(eventbus.UserEventMsgMax) {
		s.userHandler.OnMessage(msg)
	} else if msg.MsgType >= uint32(eventbus.ChannelEventMsgMin) && msg.MsgType < uint32(eventbus.ChannelEventMsgMax) {
		s.channelHandler.OnMessage(msg)
	} else if msg.MsgType >= uint32(eventbus.PushEventMsgMin) && msg.MsgType < uint32(eventbus.PushEventMsgMax) {
		s.pushHandler.OnMessage(msg)
	}
}
